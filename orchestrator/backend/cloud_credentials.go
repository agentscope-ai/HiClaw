package backend

import (
	"fmt"
	"os"

	credential "github.com/aliyun/credentials-go/credentials"
)

// CloudCredentialProvider abstracts Alibaba Cloud credential creation.
type CloudCredentialProvider interface {
	GetCredential() (credential.Credential, error)
}

// DefaultCloudCredentialProvider builds credentials from environment variables.
type DefaultCloudCredentialProvider struct{}

// NewDefaultCloudCredentialProvider creates a provider that auto-detects OIDC or AK/SK.
func NewDefaultCloudCredentialProvider() *DefaultCloudCredentialProvider {
	return &DefaultCloudCredentialProvider{}
}

func (p *DefaultCloudCredentialProvider) GetCredential() (credential.Credential, error) {
	oidcTokenFile := os.Getenv("ALIBABA_CLOUD_OIDC_TOKEN_FILE")
	if oidcTokenFile != "" {
		if _, err := os.Stat(oidcTokenFile); err == nil {
			region := envOrDefault("HICLAW_REGION", "cn-hangzhou")
			stsEndpoint := fmt.Sprintf("sts-vpc.%s.aliyuncs.com", region)
			config := new(credential.Config).
				SetType("oidc_role_arn").
				SetRoleArn(os.Getenv("ALIBABA_CLOUD_ROLE_ARN")).
				SetOIDCProviderArn(os.Getenv("ALIBABA_CLOUD_OIDC_PROVIDER_ARN")).
				SetOIDCTokenFilePath(oidcTokenFile).
				SetRoleSessionName("hiclaw-orchestrator").
				SetSTSEndpoint(stsEndpoint)
			return credential.NewCredential(config)
		}
	}

	ak := os.Getenv("ALIBABA_CLOUD_ACCESS_KEY_ID")
	if ak != "" {
		config := new(credential.Config).
			SetType("access_key").
			SetAccessKeyId(ak).
			SetAccessKeySecret(os.Getenv("ALIBABA_CLOUD_ACCESS_KEY_SECRET"))
		return credential.NewCredential(config)
	}

	return nil, fmt.Errorf("no Alibaba Cloud credentials found: set ALIBABA_CLOUD_OIDC_TOKEN_FILE or ALIBABA_CLOUD_ACCESS_KEY_ID")
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
