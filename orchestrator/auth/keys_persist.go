package auth

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// KeyPersister abstracts key storage for persistence across restarts.
type KeyPersister interface {
	Save(ctx context.Context, keys map[string]string) error
	Load(ctx context.Context) (map[string]string, error)
}

// OSSCredentialProvider provides credentials for OSS access.
type OSSCredentialProvider interface {
	GetAccessKeyId() (*string, error)
	GetAccessKeySecret() (*string, error)
	GetSecurityToken() (*string, error)
}

// OSSKeyPersister persists worker keys to an OSS JSON file.
type OSSKeyPersister struct {
	endpoint string // e.g. "oss-cn-hangzhou-internal.aliyuncs.com"
	bucket   string
	key      string // object key, e.g. "manager/orchestrator-worker-keys.json"
	creds    OSSCredentialProvider
	client   *http.Client
}

// NewOSSKeyPersister creates a persister that stores keys in OSS.
func NewOSSKeyPersister(region, bucket string, creds OSSCredentialProvider) *OSSKeyPersister {
	return &OSSKeyPersister{
		endpoint: fmt.Sprintf("oss-%s-internal.aliyuncs.com", region),
		bucket:   bucket,
		key:      "manager/orchestrator-worker-keys.json",
		creds:    creds,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (p *OSSKeyPersister) Save(ctx context.Context, keys map[string]string) error {
	data, err := json.Marshal(keys)
	if err != nil {
		return fmt.Errorf("marshal keys: %w", err)
	}

	ossURL := fmt.Sprintf("https://%s.%s/%s", p.bucket, p.endpoint, p.key)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, ossURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("build OSS PUT request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	if err := p.signRequest(req); err != nil {
		return fmt.Errorf("sign OSS request: %w", err)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("OSS PUT: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("OSS PUT failed (status %d): %s", resp.StatusCode, string(body))
	}

	log.Printf("[KeyPersister] Saved %d worker keys to OSS", len(keys))
	return nil
}

func (p *OSSKeyPersister) Load(ctx context.Context) (map[string]string, error) {
	ossURL := fmt.Sprintf("https://%s.%s/%s", p.bucket, p.endpoint, p.key)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ossURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build OSS GET request: %w", err)
	}

	if err := p.signRequest(req); err != nil {
		return nil, fmt.Errorf("sign OSS request: %w", err)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("OSS GET: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return map[string]string{}, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("OSS GET failed (status %d): %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read OSS response: %w", err)
	}

	var keys map[string]string
	if err := json.Unmarshal(body, &keys); err != nil {
		return nil, fmt.Errorf("parse keys JSON: %w", err)
	}

	log.Printf("[KeyPersister] Loaded %d worker keys from OSS", len(keys))
	return keys, nil
}

// signRequest adds OSS V1 signature headers using STS credentials.
func (p *OSSKeyPersister) signRequest(req *http.Request) error {
	ak, err := p.creds.GetAccessKeyId()
	if err != nil || ak == nil {
		return fmt.Errorf("get access key ID: %w", err)
	}
	sk, err := p.creds.GetAccessKeySecret()
	if err != nil || sk == nil {
		return fmt.Errorf("get access key secret: %w", err)
	}
	token, err := p.creds.GetSecurityToken()
	if err != nil {
		return fmt.Errorf("get security token: %w", err)
	}

	date := time.Now().UTC().Format(http.TimeFormat)
	req.Header.Set("Date", date)
	if token != nil && *token != "" {
		req.Header.Set("x-oss-security-token", *token)
	}

	contentType := req.Header.Get("Content-Type")
	resource := fmt.Sprintf("/%s/%s", p.bucket, p.key)

	canonicalHeaders := ""
	if token != nil && *token != "" {
		canonicalHeaders = "x-oss-security-token:" + *token + "\n"
	}

	stringToSign := fmt.Sprintf("%s\n\n%s\n%s\n%s%s",
		req.Method, contentType, date, canonicalHeaders, resource)

	mac := hmac.New(sha1.New, []byte(*sk))
	mac.Write([]byte(stringToSign))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	req.Header.Set("Authorization", fmt.Sprintf("OSS %s:%s", *ak, signature))
	return nil
}
