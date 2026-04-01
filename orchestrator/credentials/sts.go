package credentials

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// STSConfig holds configuration for the STS token service.
type STSConfig struct {
	Region          string
	RoleArn         string
	OIDCProviderArn string
	OIDCTokenFile   string
	OSSBucket       string
}

func (c STSConfig) endpoint() string {
	return fmt.Sprintf("https://sts-vpc.%s.aliyuncs.com", c.Region)
}

// STSService issues scoped STS tokens to workers via AssumeRoleWithOIDC.
type STSService struct {
	config           STSConfig
	httpClient       *http.Client
	endpointOverride string // for testing
}

// NewSTSService creates an STS service.
func NewSTSService(config STSConfig) *STSService {
	return &STSService{
		config:     config,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// NewSTSServiceWithClient creates an STS service with a custom HTTP client (for testing).
func NewSTSServiceWithClient(config STSConfig, client *http.Client) *STSService {
	return &STSService{
		config:     config,
		httpClient: client,
	}
}

// IssueWorkerToken calls AssumeRoleWithOIDC with an inline policy scoped to the worker.
func (s *STSService) IssueWorkerToken(ctx context.Context, workerName string) (*STSToken, error) {
	oidcToken, err := os.ReadFile(s.config.OIDCTokenFile)
	if err != nil {
		return nil, fmt.Errorf("read OIDC token file: %w", err)
	}

	policy := BuildWorkerPolicy(s.config.OSSBucket, workerName)
	endpoint := s.config.endpoint()
	if s.endpointOverride != "" {
		endpoint = s.endpointOverride
	}

	form := url.Values{
		"Action":          {"AssumeRoleWithOIDC"},
		"Format":          {"JSON"},
		"Version":         {"2015-04-01"},
		"Timestamp":       {time.Now().UTC().Format("2006-01-02T15:04:05Z")},
		"RoleArn":         {s.config.RoleArn},
		"OIDCProviderArn": {s.config.OIDCProviderArn},
		"OIDCToken":       {strings.TrimSpace(string(oidcToken))},
		"RoleSessionName": {fmt.Sprintf("hiclaw-worker-%s", workerName)},
		"DurationSeconds": {"3600"},
		"Policy":          {policy},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build STS request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("STS request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("STS returned %d: %s", resp.StatusCode, string(body))
	}

	var stsResp struct {
		Credentials struct {
			AccessKeyId     string `json:"AccessKeyId"`
			AccessKeySecret string `json:"AccessKeySecret"`
			SecurityToken   string `json:"SecurityToken"`
			Expiration      string `json:"Expiration"`
		} `json:"Credentials"`
	}
	if err := json.Unmarshal(body, &stsResp); err != nil {
		return nil, fmt.Errorf("parse STS response: %w", err)
	}

	ossEndpoint := fmt.Sprintf("oss-%s-internal.aliyuncs.com", s.config.Region)

	return &STSToken{
		AccessKeyID:     stsResp.Credentials.AccessKeyId,
		AccessKeySecret: stsResp.Credentials.AccessKeySecret,
		SecurityToken:   stsResp.Credentials.SecurityToken,
		Expiration:      stsResp.Credentials.Expiration,
		ExpiresInSec:    3600,
		OSSEndpoint:     ossEndpoint,
		OSSBucket:       s.config.OSSBucket,
	}, nil
}

// BuildWorkerPolicy generates an OSS inline policy restricting access to
// agents/{workerName}/* and shared/*.
func BuildWorkerPolicy(bucket, workerName string) string {
	policy := map[string]interface{}{
		"Version": "1",
		"Statement": []map[string]interface{}{
			{
				"Effect":   "Allow",
				"Action":   []string{"oss:ListObjects"},
				"Resource": []string{fmt.Sprintf("acs:oss:*:*:%s", bucket)},
				"Condition": map[string]interface{}{
					"StringLike": map[string]interface{}{
						"oss:Prefix": []string{
							fmt.Sprintf("agents/%s/*", workerName),
							"shared/*",
						},
					},
				},
			},
			{
				"Effect": "Allow",
				"Action": []string{"oss:GetObject", "oss:PutObject", "oss:DeleteObject"},
				"Resource": []string{
					fmt.Sprintf("acs:oss:*:*:%s/agents/%s/*", bucket, workerName),
					fmt.Sprintf("acs:oss:*:*:%s/shared/*", bucket),
				},
			},
		},
	}
	b, _ := json.Marshal(policy)
	return string(b)
}
