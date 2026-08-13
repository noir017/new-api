package aws

import (
	"context"
	"net/http"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/pkg/errors"
)

// defaultChainCredentials 缓存按 AWS 默认凭证链解析出的凭证提供者，
// 键为 "region\x00proxy"（代理不同则解析 STS 的通路不同，不能共用）。
//
// config.LoadDefaultConfig 每次调用都会重读环境变量与 ~/.aws 下的配置文件，
// 不适合摆在每个请求的路径上；而它返回的 cfg.Credentials 已被 SDK 包进
// aws.CredentialsCache —— 并发安全、按到期时间自动续期，且 web identity
// 每次续期都重读投影的 token 文件，正好对上 EKS 轮转 token 的节奏。
// 所以这里缓存提供者本体即可，不必自己做 TTL。
var defaultChainCredentials sync.Map

// getDefaultChainCredentials 解析 AWS 默认凭证链：EKS IRSA / Pod Identity、
// EC2 实例角色、ECS 任务角色、环境变量、~/.aws profile 依次尝试。
// 渠道里不保存任何静态凭证，因此凭证的生命周期完全由 SDK 自行续期。
func getDefaultChainCredentials(ctx context.Context, region string, proxy string, httpClient *http.Client) (aws.CredentialsProvider, error) {
	cacheKey := region + "\x00" + proxy
	if cached, ok := defaultChainCredentials.Load(cacheKey); ok {
		return cached.(aws.CredentialsProvider), nil
	}

	// 显式给出 region，免得 SDK 在非 EC2 环境上去 IMDS 探测 region 白等一次超时。
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithHTTPClient(httpClient),
	)
	if err != nil {
		return nil, errors.Wrap(err, "load aws default credential chain")
	}
	if cfg.Credentials == nil {
		return nil, errors.New("aws default credential chain resolved no credentials provider")
	}

	// 并发首次请求可能各解析一次，LoadOrStore 保证之后大家用的是同一个提供者。
	provider, _ := defaultChainCredentials.LoadOrStore(cacheKey, cfg.Credentials)
	return provider.(aws.CredentialsProvider), nil
}
