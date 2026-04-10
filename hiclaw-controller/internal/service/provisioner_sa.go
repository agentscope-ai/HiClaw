package service

import (
	"context"
	"fmt"

	authpkg "github.com/hiclaw/hiclaw-controller/internal/auth"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// --- ServiceAccount Management ---

// EnsureServiceAccount creates a SA + RoleBinding for the worker if it doesn't exist.
func (p *Provisioner) EnsureServiceAccount(ctx context.Context, workerName string) error {
	if p.k8sClient == nil {
		return nil
	}
	saName := authpkg.SAName(authpkg.RoleWorker, workerName)
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
				"app":              "hiclaw-worker",
				"hiclaw.io/worker": workerName,
			},
		},
	}
	if _, err := p.k8sClient.CoreV1().ServiceAccounts(ns).Create(ctx, sa, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("create SA %s: %w", saName, err)
		}
	}

	rbName := saName + "-rb"
	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rbName,
			Namespace: ns,
			Labels: map[string]string{
				"app":              "hiclaw-worker",
				"hiclaw.io/worker": workerName,
			},
		},
		Subjects: []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      saName,
			Namespace: ns,
		}},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "hiclaw-worker",
		},
	}
	if _, err := p.k8sClient.RbacV1().RoleBindings(ns).Create(ctx, rb, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("create RoleBinding %s: %w", rbName, err)
		}
	}

	return nil
}

// DeleteServiceAccount removes the SA + RoleBinding for the worker.
func (p *Provisioner) DeleteServiceAccount(ctx context.Context, workerName string) error {
	if p.k8sClient == nil {
		return nil
	}
	saName := authpkg.SAName(authpkg.RoleWorker, workerName)
	ns := p.namespace

	rbName := saName + "-rb"
	_ = p.k8sClient.RbacV1().RoleBindings(ns).Delete(ctx, rbName, metav1.DeleteOptions{})
	err := p.k8sClient.CoreV1().ServiceAccounts(ns).Delete(ctx, saName, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// EnsureManagerServiceAccount creates a SA + RoleBinding for the Manager if it doesn't exist.
func (p *Provisioner) EnsureManagerServiceAccount(ctx context.Context, managerName string) error {
	if p.k8sClient == nil {
		return nil
	}
	saName := authpkg.SAName(authpkg.RoleManager, managerName)
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
				"app":               "hiclaw-manager",
				"hiclaw.io/manager": managerName,
			},
		},
	}
	if _, err := p.k8sClient.CoreV1().ServiceAccounts(ns).Create(ctx, sa, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("create SA %s: %w", saName, err)
		}
	}

	rbName := saName + "-rb"
	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rbName,
			Namespace: ns,
			Labels: map[string]string{
				"app":               "hiclaw-manager",
				"hiclaw.io/manager": managerName,
			},
		},
		Subjects: []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      saName,
			Namespace: ns,
		}},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "hiclaw-manager",
		},
	}
	if _, err := p.k8sClient.RbacV1().RoleBindings(ns).Create(ctx, rb, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("create RoleBinding %s: %w", rbName, err)
		}
	}

	return nil
}

// DeleteManagerServiceAccount removes the SA + RoleBinding for the Manager.
func (p *Provisioner) DeleteManagerServiceAccount(ctx context.Context, managerName string) error {
	if p.k8sClient == nil {
		return nil
	}
	saName := authpkg.SAName(authpkg.RoleManager, managerName)
	ns := p.namespace

	rbName := saName + "-rb"
	_ = p.k8sClient.RbacV1().RoleBindings(ns).Delete(ctx, rbName, metav1.DeleteOptions{})
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
	saName := authpkg.SAName(authpkg.RoleManager, managerName)
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

// RequestSAToken issues a short-lived SA token for non-K8s backends (Docker/SAE).
func (p *Provisioner) RequestSAToken(ctx context.Context, workerName string) (string, error) {
	if p.k8sClient == nil {
		return "", nil
	}
	saName := authpkg.SAName(authpkg.RoleWorker, workerName)
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
