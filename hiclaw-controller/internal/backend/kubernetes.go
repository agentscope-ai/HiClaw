package backend

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const defaultK8sNamespaceFile = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"

// K8sConfig holds Kubernetes backend configuration.
type K8sConfig struct {
	Namespace        string
	WorkerImage      string
	CopawWorkerImage string
	WorkerCPU        string
	WorkerMemory     string
}

// K8sBackend manages worker lifecycle via Kubernetes Pods.
type K8sBackend struct {
	client          K8sCoreClient
	config          K8sConfig
	containerPrefix string
}

// K8sCoreClient is the minimal CoreV1 client surface needed by the backend.
type K8sCoreClient interface {
	Pods(namespace string) K8sPodClient
}

// K8sPodClient is the minimal Pod client surface needed by the backend.
type K8sPodClient interface {
	Get(ctx context.Context, name string, opts metav1.GetOptions) (*corev1.Pod, error)
	Create(ctx context.Context, pod *corev1.Pod, opts metav1.CreateOptions) (*corev1.Pod, error)
	Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error
	List(ctx context.Context, opts metav1.ListOptions) (*corev1.PodList, error)
}

// k8sCoreClientWrapper adapts *corev1client.CoreV1Client to K8sCoreClient.
type k8sCoreClientWrapper struct {
	client *corev1client.CoreV1Client
}

func (w *k8sCoreClientWrapper) Pods(namespace string) K8sPodClient {
	return w.client.Pods(namespace)
}

// NewK8sBackend creates a Kubernetes backend using in-cluster config or kubeconfig.
func NewK8sBackend(config K8sConfig, containerPrefix string) (*K8sBackend, error) {
	restConfig, err := loadK8sRESTConfig()
	if err != nil {
		return nil, err
	}
	clientset, err := corev1client.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("create kubernetes client: %w", err)
	}
	return NewK8sBackendWithClient(&k8sCoreClientWrapper{client: clientset}, config, containerPrefix), nil
}

// NewK8sBackendWithClient creates a Kubernetes backend with a custom client.
func NewK8sBackendWithClient(client K8sCoreClient, config K8sConfig, containerPrefix string) *K8sBackend {
	if containerPrefix == "" {
		containerPrefix = DefaultContainerPrefix
	}
	if config.Namespace == "" {
		config.Namespace = detectK8sNamespace()
	}
	if config.WorkerCPU == "" {
		config.WorkerCPU = "1000m"
	}
	if config.WorkerMemory == "" {
		config.WorkerMemory = "2Gi"
	}
	return &K8sBackend{
		client:          client,
		config:          config,
		containerPrefix: containerPrefix,
	}
}

func (k *K8sBackend) Name() string                   { return "k8s" }
func (k *K8sBackend) DeploymentMode() string         { return DeployCloud }
func (k *K8sBackend) NeedsCredentialInjection() bool { return true }

func (k *K8sBackend) Available(_ context.Context) bool {
	return k.client != nil && k.config.Namespace != ""
}

func (k *K8sBackend) Create(ctx context.Context, req CreateRequest) (*WorkerResult, error) {
	podName := req.ContainerName
	if podName == "" {
		podName = k.podName(req.NamePrefix, req.Name)
	}
	if _, err := k.client.Pods(k.config.Namespace).Get(ctx, podName, metav1.GetOptions{}); err == nil {
		return nil, fmt.Errorf("%w: pod %q", ErrConflict, podName)
	} else if !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("kubernetes get pod %s: %w", podName, err)
	}

	if req.Env == nil {
		req.Env = make(map[string]string)
	}
	mergeOSSRegionFromProcessEnv(req.Env)
	if rt := os.Getenv("HICLAW_RUNTIME"); rt != "" {
		req.Env["HICLAW_RUNTIME"] = rt
	} else {
		req.Env["HICLAW_RUNTIME"] = "k8s"
	}
	if req.ControllerURL != "" {
		req.Env["HICLAW_CONTROLLER_URL"] = req.ControllerURL
	}
	// SA token is mounted via projected volume; tell the worker where to read it.
	req.Env["HICLAW_AUTH_TOKEN_FILE"] = "/var/run/secrets/hiclaw/token"

	image := req.Image
	if image == "" {
		if req.Runtime == RuntimeCopaw && k.config.CopawWorkerImage != "" {
			image = k.config.CopawWorkerImage
		} else if k.config.WorkerImage != "" {
			image = k.config.WorkerImage
		}
	}
	if image == "" {
		return nil, fmt.Errorf("no worker image configured for kubernetes backend")
	}

	if req.WorkingDir == "" {
		if req.Runtime == RuntimeCopaw {
			req.WorkingDir = "/root/.copaw-worker"
		} else if home := req.Env["HOME"]; home != "" {
			req.WorkingDir = home
		} else {
			req.WorkingDir = fmt.Sprintf("/root/hiclaw-fs/agents/%s", req.Name)
			req.Env["HOME"] = req.WorkingDir
		}
	}

	cpuLimit := k.config.WorkerCPU
	memLimit := k.config.WorkerMemory
	cpuReq := "100m"
	memReq := "256Mi"
	if req.Resources != nil {
		if req.Resources.CPULimit != "" {
			cpuLimit = req.Resources.CPULimit
		}
		if req.Resources.MemoryLimit != "" {
			memLimit = req.Resources.MemoryLimit
		}
		if req.Resources.CPURequest != "" {
			cpuReq = req.Resources.CPURequest
		}
		if req.Resources.MemoryRequest != "" {
			memReq = req.Resources.MemoryRequest
		}
	}

	container := corev1.Container{
		Name:            "worker",
		Image:           image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Env:             buildK8sEnvVars(req.Env),
		Resources: corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpuLimit),
				corev1.ResourceMemory: resource.MustParse(memLimit),
			},
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpuReq),
				corev1.ResourceMemory: resource.MustParse(memReq),
			},
		},
		WorkingDir: req.WorkingDir,
	}

	tokenAudience := req.AuthAudience
	if tokenAudience == "" {
		tokenAudience = "hiclaw-controller"
	}
	tokenExpSeconds := int64(3600)
	projectedVol := corev1.Volume{
		Name: "hiclaw-token",
		VolumeSource: corev1.VolumeSource{
			Projected: &corev1.ProjectedVolumeSource{
				Sources: []corev1.VolumeProjection{{
					ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
						Audience:          tokenAudience,
						ExpirationSeconds: &tokenExpSeconds,
						Path:              "token",
					},
				}},
			},
		},
	}
	container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
		Name:      "hiclaw-token",
		MountPath: "/var/run/secrets/hiclaw",
		ReadOnly:  true,
	})

	saName := req.ServiceAccountName
	if saName == "" {
		saName = "hiclaw-worker-" + req.Name
	}
	podSpec := corev1.PodSpec{
		Containers:                   []corev1.Container{container},
		RestartPolicy:                corev1.RestartPolicyAlways,
		ServiceAccountName:           saName,
		AutomountServiceAccountToken: boolPtr(false), // disable default mount; using projected volume with custom audience instead
		Volumes:                      []corev1.Volume{projectedVol},
	}
	if tolerations := k.getCurrentPodTolerations(ctx); len(tolerations) > 0 {
		podSpec.Tolerations = tolerations
	}
	if imagePullSecrets := k.getCurrentPodImagePullSecrets(ctx); len(imagePullSecrets) > 0 {
		podSpec.ImagePullSecrets = imagePullSecrets
	}
	if hostAliases := buildHostAliases(req.ExtraHosts); len(hostAliases) > 0 {
		podSpec.HostAliases = hostAliases
	}

	podLabels := map[string]string{
		"hiclaw.io/runtime": defaultRuntime(req.Runtime),
	}
	for k, v := range req.Labels {
		podLabels[k] = v
	}
	if podLabels["app"] == "" {
		podLabels["app"] = "hiclaw-worker"
	}
	if _, hasManager := podLabels["hiclaw.io/manager"]; !hasManager {
		if podLabels["hiclaw.io/worker"] == "" {
			podLabels["hiclaw.io/worker"] = req.Name
		}
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: k.config.Namespace,
			Labels:    podLabels,
			Annotations: map[string]string{
				"hiclaw.io/created-by": "controller",
			},
		},
		Spec: podSpec,
	}

	created, err := k.client.Pods(k.config.Namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			return nil, fmt.Errorf("%w: pod %q", ErrConflict, podName)
		}
		return nil, fmt.Errorf("kubernetes create pod %s: %w", podName, err)
	}

	return &WorkerResult{
		Name:      req.Name,
		Backend:   "k8s",
		Status:    StatusStarting,
		RawStatus: rawK8sPhase(created.Status.Phase),
	}, nil
}

func (k *K8sBackend) Delete(ctx context.Context, name string) error {
	podName := k.workerPodName(name)
	err := k.client.Pods(k.config.Namespace).Delete(ctx, podName, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("kubernetes delete pod %s: %w", podName, err)
	}
	return nil
}

func (k *K8sBackend) Start(ctx context.Context, name string) error {
	pod, err := k.client.Pods(k.config.Namespace).Get(ctx, k.workerPodName(name), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return fmt.Errorf("%w: worker %q", ErrNotFound, name)
	}
	if err != nil {
		return fmt.Errorf("kubernetes get pod %s: %w", k.workerPodName(name), err)
	}

	switch pod.Status.Phase {
	case corev1.PodRunning, corev1.PodPending:
		return nil
	default:
		return fmt.Errorf("kubernetes worker %q cannot be started from phase %q; recreate it instead", name, pod.Status.Phase)
	}
}

func (k *K8sBackend) Stop(ctx context.Context, name string) error {
	return k.Delete(ctx, name)
}

func (k *K8sBackend) Status(ctx context.Context, name string) (*WorkerResult, error) {
	pod, err := k.client.Pods(k.config.Namespace).Get(ctx, k.workerPodName(name), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return &WorkerResult{Name: name, Backend: "k8s", Status: StatusNotFound}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("kubernetes get pod %s: %w", k.workerPodName(name), err)
	}
	return &WorkerResult{
		Name:           name,
		Backend:        "k8s",
		DeploymentMode: DeployCloud,
		Status:         normalizeK8sPodPhase(pod.Status.Phase),
		RawStatus:      rawK8sPhase(pod.Status.Phase),
	}, nil
}

func (k *K8sBackend) List(ctx context.Context) ([]WorkerResult, error) {
	pods, err := k.client.Pods(k.config.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=hiclaw-worker",
	})
	if err != nil {
		return nil, fmt.Errorf("kubernetes list worker pods: %w", err)
	}

	results := make([]WorkerResult, 0, len(pods.Items))
	for _, pod := range pods.Items {
		name := pod.Labels["hiclaw.io/worker"]
		if name == "" {
			name = strings.TrimPrefix(pod.Name, k.containerPrefix)
		}
		results = append(results, WorkerResult{
			Name:           name,
			Backend:        "k8s",
			DeploymentMode: DeployCloud,
			Status:         normalizeK8sPodPhase(pod.Status.Phase),
			RawStatus:      rawK8sPhase(pod.Status.Phase),
		})
	}
	return results, nil
}

func (k *K8sBackend) podName(prefix, name string) string {
	if prefix != "" {
		return prefix + name
	}
	return k.containerPrefix + name
}

func (k *K8sBackend) workerPodName(name string) string {
	return k.containerPrefix + name
}

func (k *K8sBackend) getCurrentPod(ctx context.Context) *corev1.Pod {
	hostname := strings.TrimSpace(os.Getenv("HOSTNAME"))
	if hostname == "" || k.config.Namespace == "" {
		return nil
	}
	pod, err := k.client.Pods(k.config.Namespace).Get(ctx, hostname, metav1.GetOptions{})
	if err != nil {
		return nil
	}
	return pod
}

func (k *K8sBackend) getCurrentPodTolerations(ctx context.Context) []corev1.Toleration {
	pod := k.getCurrentPod(ctx)
	if pod == nil {
		return nil
	}
	return append([]corev1.Toleration(nil), pod.Spec.Tolerations...)
}

func (k *K8sBackend) getCurrentPodImagePullSecrets(ctx context.Context) []corev1.LocalObjectReference {
	pod := k.getCurrentPod(ctx)
	if pod == nil {
		return nil
	}
	return append([]corev1.LocalObjectReference(nil), pod.Spec.ImagePullSecrets...)
}

// mergeOSSRegionFromProcessEnv sets HICLAW_FS_BUCKET and HICLAW_REGION when the client
// omitted them; the controller process should already have these from the same Secret as Manager (envFrom).
func mergeOSSRegionFromProcessEnv(env map[string]string) {
	if env == nil {
		return
	}
	bucket := firstNonEmptyTrimmed(
		env["HICLAW_FS_BUCKET"],
		os.Getenv("HICLAW_FS_BUCKET"),
	)
	if bucket != "" && strings.TrimSpace(env["HICLAW_FS_BUCKET"]) == "" {
		env["HICLAW_FS_BUCKET"] = bucket
	}
	if v := strings.TrimSpace(os.Getenv("HICLAW_REGION")); v != "" && strings.TrimSpace(env["HICLAW_REGION"]) == "" {
		env["HICLAW_REGION"] = v
	}
}

func firstNonEmptyTrimmed(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func buildK8sEnvVars(env map[string]string) []corev1.EnvVar {
	keys := make([]string, 0, len(env))
	for k := range env {
		if env[k] != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	var out []corev1.EnvVar
	for _, k := range keys {
		out = append(out, corev1.EnvVar{Name: k, Value: env[k]})
	}
	return out
}

func buildHostAliases(extraHosts []string) []corev1.HostAlias {
	byIP := map[string][]string{}
	for _, entry := range extraHosts {
		host, ip, ok := strings.Cut(strings.TrimSpace(entry), ":")
		if !ok || host == "" || ip == "" {
			continue
		}
		byIP[ip] = append(byIP[ip], host)
	}
	if len(byIP) == 0 {
		return nil
	}

	ips := make([]string, 0, len(byIP))
	for ip := range byIP {
		ips = append(ips, ip)
	}
	sort.Strings(ips)

	aliases := make([]corev1.HostAlias, 0, len(ips))
	for _, ip := range ips {
		hosts := byIP[ip]
		sort.Strings(hosts)
		aliases = append(aliases, corev1.HostAlias{
			IP:        ip,
			Hostnames: hosts,
		})
	}
	return aliases
}

func normalizeK8sPodPhase(phase corev1.PodPhase) WorkerStatus {
	switch phase {
	case corev1.PodRunning:
		return StatusRunning
	case corev1.PodPending:
		return StatusStarting
	case corev1.PodSucceeded, corev1.PodFailed:
		return StatusStopped
	default:
		return StatusUnknown
	}
}

func rawK8sPhase(phase corev1.PodPhase) string {
	if phase == "" {
		return "Pending"
	}
	return string(phase)
}

func defaultRuntime(runtime string) string {
	if runtime == RuntimeCopaw {
		return RuntimeCopaw
	}
	return RuntimeOpenClaw
}

func loadK8sRESTConfig() (*rest.Config, error) {
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = clientcmd.RecommendedHomeFile
	}
	if _, err := os.Stat(kubeconfig); err != nil {
		return nil, fmt.Errorf("load kubernetes config: no in-cluster config and kubeconfig %q not found", kubeconfig)
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("load kubernetes kubeconfig %q: %w", kubeconfig, err)
	}
	return cfg, nil
}

func detectK8sNamespace() string {
	if ns := strings.TrimSpace(os.Getenv("HICLAW_K8S_NAMESPACE")); ns != "" {
		return ns
	}
	if data, err := os.ReadFile(defaultK8sNamespaceFile); err == nil {
		if ns := strings.TrimSpace(string(data)); ns != "" {
			return ns
		}
	}
	return ""
}

func boolPtr(v bool) *bool {
	return &v
}
