package config

import (
	"fmt"
	"math"

	osutils "github.com/kyverno/kyverno/pkg/utils/os"
	rest "k8s.io/client-go/rest"
	clientcmd "k8s.io/client-go/tools/clientcmd"
)

// These constants MUST be equal to the corresponding names in service definition in definitions/install.yaml
const (
	//MutatingWebhookConfigurationName default resource mutating webhook configuration name
	MutatingWebhookConfigurationName = "kyverno-resource-mutating-webhook-cfg"
	//MutatingWebhookConfigurationDebugName default resource mutating webhook configuration name for debug mode
	MutatingWebhookConfigurationDebugName = "kyverno-resource-mutating-webhook-cfg-debug"
	//MutatingWebhookName default resource mutating webhook name
	MutatingWebhookName = "mutate.kyverno.svc"

	ValidatingWebhookConfigurationName      = "kyverno-resource-validating-webhook-cfg"
	ValidatingWebhookConfigurationDebugName = "kyverno-resource-validating-webhook-cfg-debug"
	ValidatingWebhookName                   = "validate.kyverno.svc"

	//VerifyMutatingWebhookConfigurationName default verify mutating webhook configuration name
	VerifyMutatingWebhookConfigurationName = "kyverno-verify-mutating-webhook-cfg"
	//VerifyMutatingWebhookConfigurationDebugName default verify mutating webhook configuration name for debug mode
	VerifyMutatingWebhookConfigurationDebugName = "kyverno-verify-mutating-webhook-cfg-debug"
	//VerifyMutatingWebhookName default verify mutating webhook name
	VerifyMutatingWebhookName = "monitor-webhooks.kyverno.svc"

	//PolicyValidatingWebhookConfigurationName default policy validating webhook configuration name
	PolicyValidatingWebhookConfigurationName = "kyverno-policy-validating-webhook-cfg"
	//PolicyValidatingWebhookConfigurationDebugName default policy validating webhook configuration name for debug mode
	PolicyValidatingWebhookConfigurationDebugName = "kyverno-policy-validating-webhook-cfg-debug"
	//PolicyValidatingWebhookName default policy validating webhook name
	PolicyValidatingWebhookName = "validate-policy.kyverno.svc"

	//PolicyMutatingWebhookConfigurationName default policy mutating webhook configuration name
	PolicyMutatingWebhookConfigurationName = "kyverno-policy-mutating-webhook-cfg"
	//PolicyMutatingWebhookConfigurationDebugName default policy mutating webhook configuration name for debug mode
	PolicyMutatingWebhookConfigurationDebugName = "kyverno-policy-mutating-webhook-cfg-debug"
	//PolicyMutatingWebhookName default policy mutating webhook name
	PolicyMutatingWebhookName = "mutate-policy.kyverno.svc"

	// Due to kubernetes issue, we must use next literal constants instead of deployment TypeMeta fields
	// Issue: https://github.com/kubernetes/kubernetes/pull/63972
	// When the issue is closed, we should use TypeMeta struct instead of this constants

	// DeploymentKind define the default deployment resource kind
	DeploymentKind = "Deployment"

	// DeploymentAPIVersion define the default deployment resource apiVersion
	DeploymentAPIVersion = "apps/v1"

	// NamespaceKind define the default namespace resource kind
	NamespaceKind = "Namespace"

	// NamespaceAPIVersion define the default namespace resource apiVersion
	NamespaceAPIVersion = "v1"

	// ClusterRoleAPIVersion define the default clusterrole resource apiVersion
	ClusterRoleAPIVersion = "rbac.authorization.k8s.io/v1"

	// ClusterRoleKind define the default clusterrole resource kind
	ClusterRoleKind = "ClusterRole"
	//MutatingWebhookServicePath is the path for mutation webhook
	MutatingWebhookServicePath = "/mutate"

	//ValidatingWebhookServicePath is the path for validation webhook
	ValidatingWebhookServicePath = "/validate"

	//PolicyValidatingWebhookServicePath is the path for policy validation webhook(used to validate policy resource)
	PolicyValidatingWebhookServicePath = "/policyvalidate"

	//PolicyMutatingWebhookServicePath is the path for policy mutation webhook(used to default)
	PolicyMutatingWebhookServicePath = "/policymutate"

	//VerifyMutatingWebhookServicePath is the path for verify webhook(used to veryfing if admission control is enabled and active)
	VerifyMutatingWebhookServicePath = "/verifymutate"

	// LivenessServicePath is the path for check liveness health
	LivenessServicePath = "/health/liveness"

	// ReadinessServicePath is the path for check readness health
	ReadinessServicePath = "/health/readiness"
)

var (
	//KyvernoNamespace is the Kyverno namespace
	KyvernoNamespace = osutils.GetEnvWithFallback("KYVERNO_NAMESPACE", "kyverno")
	// KyvernoDeploymentName is the Kyverno deployment name
	KyvernoDeploymentName = osutils.GetEnvWithFallback("KYVERNO_DEPLOYMENT", "kyverno")
	//KyvernoServiceName is the Kyverno service name
	KyvernoServiceName = osutils.GetEnvWithFallback("KYVERNO_SVC", "kyverno-svc")
)

//CreateClientConfig creates client config and applies rate limit QPS and burst
func CreateClientConfig(kubeconfig string, qps float64, burst int) (*rest.Config, error) {
	clientConfig, err := createClientConfig(kubeconfig)
	if err != nil {
		return nil, err
	}

	if qps > math.MaxFloat32 {
		return nil, fmt.Errorf("client rate limit QPS must not be higher than %e", math.MaxFloat32)
	}
	clientConfig.Burst = burst
	clientConfig.QPS = float32(qps)

	return clientConfig, nil
}

// createClientConfig creates client config
func createClientConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig == "" {
		logger.Info("Using in-cluster configuration")
		return rest.InClusterConfig()
	}
	logger.V(4).Info("Using specified kubeconfig", "kubeconfig", kubeconfig)
	return clientcmd.BuildConfigFromFlags("", kubeconfig)
}
