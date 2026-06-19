package kube

import (
	"bytes"
	"context"
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/cbartram/rekja/internal/config"
)

// Client provides the Kubernetes operations Rekja needs.
type Client interface {
	ResolveTarget(ctx context.Context) (Target, error)
	Restart(ctx context.Context, target Target) (string, error)
	Logs(ctx context.Context, target Target, lines int64) (string, error)
}

// Target identifies the Valheim container.
type Target struct {
	Namespace string
	Pod       string
	Container string
}

type realClient struct {
	cfg       config.KubernetesConfig
	rest      *rest.Config
	clientset *kubernetes.Clientset
}

type noopClient struct {
	cfg config.KubernetesConfig
}

// New creates an in-cluster or kubeconfig-backed Kubernetes client. If no
// target hints are configured and no cluster config exists, it returns a no-op
// client so local inventory/testing still works.
func New(cfg config.KubernetesConfig) (Client, error) {
	restConfig, err := rest.InClusterConfig()
	if err != nil {
		if cfg.KubeconfigPath == "" {
			if home := os.Getenv("HOME"); home != "" {
				cfg.KubeconfigPath = home + "/.kube/config"
			}
		}
		restConfig, err = clientcmd.BuildConfigFromFlags("", cfg.KubeconfigPath)
		if err != nil {
			return noopClient{cfg: cfg}, nil
		}
	}
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, err
	}
	return &realClient{cfg: cfg, rest: restConfig, clientset: clientset}, nil
}

func (c noopClient) ResolveTarget(ctx context.Context) (Target, error) {
	return Target{
		Namespace: c.cfg.Namespace,
		Pod:       c.cfg.PodName,
		Container: c.cfg.ContainerName,
	}, nil
}

func (c noopClient) Restart(ctx context.Context, target Target) (string, error) {
	return "", fmt.Errorf("kubernetes client is not configured")
}

func (c noopClient) Logs(ctx context.Context, target Target, lines int64) (string, error) {
	return "", fmt.Errorf("kubernetes client is not configured")
}

func (c *realClient) ResolveTarget(ctx context.Context) (Target, error) {
	namespace := c.cfg.Namespace
	if namespace == "" {
		namespace = "default"
	}
	if c.cfg.PodName != "" {
		return Target{Namespace: namespace, Pod: c.cfg.PodName, Container: c.cfg.ContainerName}, nil
	}
	if c.cfg.LabelSelector == "" {
		return Target{}, fmt.Errorf("pod_name or label_selector is required for kubernetes actions")
	}
	pods, err := c.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: c.cfg.LabelSelector})
	if err != nil {
		return Target{}, err
	}
	if len(pods.Items) != 1 {
		return Target{}, fmt.Errorf("expected exactly one pod for selector %q, found %d", c.cfg.LabelSelector, len(pods.Items))
	}
	return Target{Namespace: namespace, Pod: pods.Items[0].Name, Container: c.cfg.ContainerName}, nil
}

func (c *realClient) Restart(ctx context.Context, target Target) (string, error) {
	command := c.cfg.RestartCommand
	if len(command) == 0 {
		command = []string{"supervisorctl", "restart", "valheim-server"}
	}
	return c.exec(ctx, target, command)
}

func (c *realClient) Logs(ctx context.Context, target Target, lines int64) (string, error) {
	request := c.clientset.CoreV1().Pods(target.Namespace).GetLogs(target.Pod, &corev1.PodLogOptions{
		Container: target.Container,
		TailLines: &lines,
	})
	body, err := request.Stream(ctx)
	if err != nil {
		return "", err
	}
	defer body.Close()

	var buffer bytes.Buffer
	if _, err := buffer.ReadFrom(body); err != nil {
		return "", err
	}
	return buffer.String(), nil
}

func (c *realClient) exec(ctx context.Context, target Target, command []string) (string, error) {
	request := c.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(target.Pod).
		Namespace(target.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: target.Container,
			Command:   command,
			Stdout:    true,
			Stderr:    true,
		}, metav1.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(c.rest, "POST", request.URL())
	if err != nil {
		return "", err
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		return stdout.String() + stderr.String(), err
	}
	return stdout.String() + stderr.String(), nil
}
