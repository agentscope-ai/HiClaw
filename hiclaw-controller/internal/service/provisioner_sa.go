package service

import (
	"context"
	"fmt"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	authpkg "github.com/hiclaw/hiclaw-controller/internal/auth"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// --- ServiceAccount Management ---

// EnsureServiceAccount creates a ServiceAccount for the worker if it doesn't exist.
func (p *Provisioner) EnsureServiceAccount(ctx context.Context, workerName string) error {
	if p.k8sClient == nil {
		return nil
	}
	saName := p.resourcePrefix.SAName(authpkg.RoleWorker, workerName)
	ns := p.namespace

	_, err := p.k8sClient.CoreV1().ServiceAccounts(ns).Get(ctx, saName, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get SA %s: %w", saName, err)
	}

	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      saName,
			Namespace: ns,
			Labels: map[string]string{
				"app":                   p.resourcePrefix.WorkerAppLabel(),
				"hiclaw.io/worker":      workerName,
				v1beta1.LabelController: p.controllerName,
			},
		},
	}
	if _, err := p.k8sClient.CoreV1().ServiceAccounts(ns).Create(ctx, sa, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("create SA %s: %w", saName, err)
		}
	}

	return nil
}

// DeleteServiceAccount removes the ServiceAccount for the worker.
func (p *Provisioner) DeleteServiceAccount(ctx context.Context, workerName string) error {
	if p.k8sClient == nil {
		return nil
	}
	saName := p.resourcePrefix.SAName(authpkg.RoleWorker, workerName)
	ns := p.namespace

	err := p.k8sClient.CoreV1().ServiceAccounts(ns).Delete(ctx, saName, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// EnsureManagerServiceAccount creates a ServiceAccount for the Manager if it doesn't exist.
func (p *Provisioner) EnsureManagerServiceAccount(ctx context.Context, managerName string) error {
	if p.k8sClient == nil {
		return nil
	}
	saName := p.resourcePrefix.SAName(authpkg.RoleManager, managerName)
	ns := p.namespace

	_, err := p.k8sClient.CoreV1().ServiceAccounts(ns).Get(ctx, saName, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get SA %s: %w", saName, err)
	}

	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      saName,
			Namespace: ns,
			Labels: map[string]string{
				"app":                   p.resourcePrefix.ManagerAppLabel(),
				"hiclaw.io/manager":     managerName,
				v1beta1.LabelController: p.controllerName,
			},
		},
	}
	if _, err := p.k8sClient.CoreV1().ServiceAccounts(ns).Create(ctx, sa, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("create SA %s: %w", saName, err)
		}
	}

	return nil
}

// DeleteManagerServiceAccount removes the ServiceAccount for the Manager.
func (p *Provisioner) DeleteManagerServiceAccount(ctx context.Context, managerName string) error {
	if p.k8sClient == nil {
		return nil
	}
	saName := p.resourcePrefix.SAName(authpkg.RoleManager, managerName)
	ns := p.namespace

	err := p.k8sClient.CoreV1().ServiceAccounts(ns).Delete(ctx, saName, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// RequestManagerSAToken issues a short-lived SA token for Manager in non-K8s backends.
func (p *Provisioner) RequestManagerSAToken(ctx context.Context, managerName string) (string, error) {
	if p.k8sClient == nil {
		return "", nil
	}
	saName := p.resourcePrefix.SAName(authpkg.RoleManager, managerName)
	audience := p.authAudience
	if audience == "" {
		audience = authpkg.DefaultAudience
	}
	expSeconds := int64(315360000)

	tokenReq := &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{
			Audiences:         []string{audience},
			ExpirationSeconds: &expSeconds,
		},
	}

	result, err := p.k8sClient.CoreV1().ServiceAccounts(p.namespace).CreateToken(
		ctx, saName, tokenReq, metav1.CreateOptions{},
	)
	if err != nil {
		return "", fmt.Errorf("request SA token for manager %s: %w", managerName, err)
	}
	return result.Status.Token, nil
}

// EnsureAdminServiceAccount creates the admin ServiceAccount if it doesn't exist.
//
// The admin SA exists primarily so the bundled `hiclaw` CLI inside the
// hiclaw-controller container (and any other operator tooling that runs in
// the same trust boundary, e.g. install-time `docker exec hiclaw-controller
// hiclaw …`) can authenticate against the controller's HTTP API with
// admin-role privileges via the standard TokenReview path. Without it the
// only paths to call the API would be (a) reusing a Manager- or Worker-scoped
// SA token (wrong privilege scope, and not always available locally) or
// (b) using the embedded kube-apiserver's static admin token (a different
// token format that `prefix.ParseSAUsername` rejects with HTTP 401, since
// it surfaces as username `admin` rather than `system:serviceaccount:…`).
//
// Idempotent: returns nil if the SA already exists. Returns nil silently
// when k8sClient is nil (incluster-without-bootstrap or unit-test paths).
func (p *Provisioner) EnsureAdminServiceAccount(ctx context.Context) error {
	if p.k8sClient == nil {
		return nil
	}
	saName := p.resourcePrefix.AdminName()
	ns := p.namespace

	_, err := p.k8sClient.CoreV1().ServiceAccounts(ns).Get(ctx, saName, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get SA %s: %w", saName, err)
	}

	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      saName,
			Namespace: ns,
			Labels: map[string]string{
				"hiclaw.io/role": authpkg.RoleAdmin,
			},
		},
	}
	if _, err := p.k8sClient.CoreV1().ServiceAccounts(ns).Create(ctx, sa, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("create SA %s: %w", saName, err)
		}
	}

	return nil
}

// RequestAdminSAToken issues a long-lived SA token for the admin SA.
//
// The token is written by the embedded-mode startup path to a known
// location (see `internal/app.bootstrapAdminCLIToken`) so that the bundled
// `hiclaw` CLI can auto-discover it via the `HICLAW_AUTH_TOKEN_FILE`
// environment variable that the controller image sets by default.
//
// Expiration mirrors `RequestManagerSAToken` (~10 years) — the controller
// re-mints on every startup, so any reasonable lower bound would also
// work; we keep the long horizon to avoid surprise auth failures during
// long-running interactive shells when the operator forgets to re-source.
func (p *Provisioner) RequestAdminSAToken(ctx context.Context) (string, error) {
	if p.k8sClient == nil {
		return "", nil
	}
	saName := p.resourcePrefix.AdminName()
	audience := p.authAudience
	if audience == "" {
		audience = authpkg.DefaultAudience
	}
	expSeconds := int64(315360000)

	tokenReq := &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{
			Audiences:         []string{audience},
			ExpirationSeconds: &expSeconds,
		},
	}

	result, err := p.k8sClient.CoreV1().ServiceAccounts(p.namespace).CreateToken(
		ctx, saName, tokenReq, metav1.CreateOptions{},
	)
	if err != nil {
		return "", fmt.Errorf("request SA token for admin: %w", err)
	}
	return result.Status.Token, nil
}

// RequestSAToken issues a short-lived SA token for non-K8s backends (Docker).
func (p *Provisioner) RequestSAToken(ctx context.Context, workerName string) (string, error) {
	if p.k8sClient == nil {
		return "", nil
	}
	saName := p.resourcePrefix.SAName(authpkg.RoleWorker, workerName)
	audience := p.authAudience
	if audience == "" {
		audience = authpkg.DefaultAudience
	}
	expSeconds := int64(315360000) // 24h

	tokenReq := &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{
			Audiences:         []string{audience},
			ExpirationSeconds: &expSeconds,
		},
	}

	result, err := p.k8sClient.CoreV1().ServiceAccounts(p.namespace).CreateToken(
		ctx, saName, tokenReq, metav1.CreateOptions{},
	)
	if err != nil {
		return "", fmt.Errorf("request SA token for %s: %w", workerName, err)
	}
	return result.Status.Token, nil
}
