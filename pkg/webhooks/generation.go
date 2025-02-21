package webhooks

import (
	contextdefault "context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gardener/controller-manager-library/pkg/logger"
	"github.com/go-logr/logr"
	kyverno "github.com/kyverno/kyverno/api/kyverno/v1"
	urkyverno "github.com/kyverno/kyverno/api/kyverno/v1beta1"
	"github.com/kyverno/kyverno/pkg/autogen"
	gencommon "github.com/kyverno/kyverno/pkg/background/common"
	gen "github.com/kyverno/kyverno/pkg/background/generate"
	"github.com/kyverno/kyverno/pkg/common"
	"github.com/kyverno/kyverno/pkg/config"
	client "github.com/kyverno/kyverno/pkg/dclient"
	"github.com/kyverno/kyverno/pkg/engine"
	enginectx "github.com/kyverno/kyverno/pkg/engine/context"
	"github.com/kyverno/kyverno/pkg/engine/response"
	enginutils "github.com/kyverno/kyverno/pkg/engine/utils"
	"github.com/kyverno/kyverno/pkg/engine/variables"
	"github.com/kyverno/kyverno/pkg/event"
	jsonutils "github.com/kyverno/kyverno/pkg/utils/json"
	"github.com/kyverno/kyverno/pkg/webhooks/updaterequest"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
)

//handleGenerate handles admission-requests for policies with generate rules
func (ws *WebhookServer) handleGenerate(
	request *admissionv1.AdmissionRequest,
	policies []kyverno.PolicyInterface,
	policyContext *engine.PolicyContext,
	admissionRequestTimestamp int64,
	latencySender *chan int64,
	generateEngineResponsesSenderForAdmissionReviewDurationMetric *chan []*response.EngineResponse,
	generateEngineResponsesSenderForAdmissionRequestsCountMetric *chan []*response.EngineResponse,
) {

	logger := ws.log.WithValues("action", "generation", "uid", request.UID, "kind", request.Kind, "namespace", request.Namespace, "name", request.Name, "operation", request.Operation, "gvk", request.Kind.String())
	logger.V(6).Info("update request")

	var engineResponses []*response.EngineResponse
	if (request.Operation == admissionv1.Create || request.Operation == admissionv1.Update) && len(policies) != 0 {
		for _, policy := range policies {
			var rules []response.RuleResponse
			policyContext.Policy = policy
			if request.Kind.Kind != "Namespace" && request.Namespace != "" {
				policyContext.NamespaceLabels = common.GetNamespaceSelectorsFromNamespaceLister(request.Kind.Kind, request.Namespace, ws.nsLister, logger)
			}
			engineResponse := engine.ApplyBackgroundChecks(policyContext)
			for _, rule := range engineResponse.PolicyResponse.Rules {
				if rule.Status != response.RuleStatusPass {
					ws.deleteGR(logger, engineResponse)
					continue
				}
				rules = append(rules, rule)
			}

			if len(rules) > 0 {
				engineResponse.PolicyResponse.Rules = rules
				// some generate rules do apply to the resource
				engineResponses = append(engineResponses, engineResponse)
			}

			// registering the kyverno_policy_results_total metric concurrently
			go ws.registerPolicyResultsMetricGeneration(logger, string(request.Operation), policy, *engineResponse)

			// registering the kyverno_policy_execution_duration_seconds metric concurrently
			go ws.registerPolicyExecutionDurationMetricGenerate(logger, string(request.Operation), policy, *engineResponse)
		}

		if failedResponse := applyGenerateRequest(request, urkyverno.Generate, ws.grGenerator, policyContext.AdmissionInfo, request.Operation, engineResponses...); failedResponse != nil {
			// report failure event
			for _, failedGR := range failedResponse {
				events := failedEvents(fmt.Errorf("failed to create Generate Request: %v", failedGR.err), failedGR.gr, policyContext.NewResource)
				ws.eventGen.Add(events...)
			}
		}
	}

	if request.Operation == admissionv1.Update {
		ws.handleUpdatesForGenerateRules(request, policies)
	}

	// sending the admission request latency to other goroutine (reporting the metrics) over the channel
	admissionReviewLatencyDuration := int64(time.Since(time.Unix(admissionRequestTimestamp, 0)))
	*latencySender <- admissionReviewLatencyDuration
	*generateEngineResponsesSenderForAdmissionReviewDurationMetric <- engineResponses
	*generateEngineResponsesSenderForAdmissionRequestsCountMetric <- engineResponses
}

//handleUpdatesForGenerateRules handles admission-requests for update
func (ws *WebhookServer) handleUpdatesForGenerateRules(request *admissionv1.AdmissionRequest, policies []kyverno.PolicyInterface) {
	if request.Operation != admissionv1.Update {
		return
	}

	logger := ws.log.WithValues("action", "generate", "uid", request.UID, "kind", request.Kind, "namespace", request.Namespace, "name", request.Name, "operation", request.Operation, "gvk", request.Kind.String())
	resource, err := enginutils.ConvertToUnstructured(request.OldObject.Raw)
	if err != nil {
		logger.Error(err, "failed to convert object resource to unstructured format")
	}

	resLabels := resource.GetLabels()
	if resLabels["generate.kyverno.io/clone-policy-name"] != "" {
		ws.handleUpdateGenerateSourceResource(resLabels, logger)
	}

	if resLabels["app.kubernetes.io/managed-by"] == "kyverno" && resLabels["policy.kyverno.io/synchronize"] == "enable" && request.Operation == admissionv1.Update {
		ws.handleUpdateGenerateTargetResource(request, policies, resLabels, logger)
	}
}

//handleUpdateGenerateSourceResource - handles update of clone source for generate policy
func (ws *WebhookServer) handleUpdateGenerateSourceResource(resLabels map[string]string, logger logr.Logger) {
	policyNames := strings.Split(resLabels["generate.kyverno.io/clone-policy-name"], ",")
	for _, policyName := range policyNames {

		// check if the policy exists
		_, err := ws.kyvernoClient.KyvernoV1().ClusterPolicies().Get(contextdefault.TODO(), policyName, metav1.GetOptions{})
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				logger.V(4).Info("skipping update of generate request as policy is deleted")
			} else {
				logger.Error(err, "failed to get generate policy", "Name", policyName)
			}
		} else {
			selector := labels.SelectorFromSet(labels.Set(map[string]string{
				urkyverno.URGeneratePolicyLabel: policyName,
			}))

			grList, err := ws.urLister.List(selector)
			if err != nil {
				logger.Error(err, "failed to get generate request for the resource", "label", urkyverno.URGeneratePolicyLabel)
				return
			}

			for _, gr := range grList {
				ws.updateAnnotationInGR(gr, logger)
			}
		}

	}
}

// updateAnnotationInGR - function used to update GR annotation
// updating GR will trigger reprocessing of GR and recreation/updation of generated resource
func (ws *WebhookServer) updateAnnotationInGR(gr *urkyverno.UpdateRequest, logger logr.Logger) {
	grAnnotations := gr.Annotations
	if len(grAnnotations) == 0 {
		grAnnotations = make(map[string]string)
	}
	ws.mu.Lock()
	grAnnotations["generate.kyverno.io/updation-time"] = time.Now().String()
	gr.SetAnnotations(grAnnotations)
	ws.mu.Unlock()

	patch := jsonutils.NewPatch(
		"/metadata/annotations",
		"replace",
		gr.Annotations,
	)

	new, err := gencommon.PatchGenerateRequest(gr, patch, ws.kyvernoClient)
	if err != nil {
		logger.Error(err, "failed to update generate request update-time annotations for the resource", "generate request", gr.Name)
		return
	}
	new.Status.State = urkyverno.Pending
	if _, err := ws.kyvernoClient.KyvernoV1beta1().UpdateRequests(config.KyvernoNamespace).UpdateStatus(contextdefault.TODO(), new, metav1.UpdateOptions{}); err != nil {
		logger.Error(err, "failed to set UpdateRequest state to Pending", "update request", gr.Name)
	}
}

//handleUpdateGenerateTargetResource - handles update of target resource for generate policy
func (ws *WebhookServer) handleUpdateGenerateTargetResource(request *admissionv1.AdmissionRequest, policies []kyverno.PolicyInterface, resLabels map[string]string, logger logr.Logger) {
	enqueueBool := false
	newRes, err := enginutils.ConvertToUnstructured(request.Object.Raw)
	if err != nil {
		logger.Error(err, "failed to convert object resource to unstructured format")
	}

	policyName := resLabels["policy.kyverno.io/policy-name"]
	targetSourceName := newRes.GetName()
	targetSourceKind := newRes.GetKind()

	policy, err := ws.kyvernoClient.KyvernoV1().ClusterPolicies().Get(contextdefault.TODO(), policyName, metav1.GetOptions{})
	if err != nil {
		logger.Error(err, "failed to get policy from kyverno client.", "policy name", policyName)
		return
	}

	for _, rule := range autogen.ComputeRules(policy) {
		if rule.Generation.Kind == targetSourceKind && rule.Generation.Name == targetSourceName {
			updatedRule, err := getGeneratedByResource(newRes, resLabels, ws.client, rule, logger)
			if err != nil {
				logger.V(4).Info("skipping generate policy and resource pattern validaton", "error", err)
			} else {
				data := updatedRule.Generation.DeepCopy().GetData()
				if data != nil {
					if _, err := gen.ValidateResourceWithPattern(logger, newRes.Object, data); err != nil {
						enqueueBool = true
						break
					}
				}

				cloneName := updatedRule.Generation.Clone.Name
				if cloneName != "" {
					obj, err := ws.client.GetResource("", rule.Generation.Kind, rule.Generation.Clone.Namespace, rule.Generation.Clone.Name)
					if err != nil {
						logger.Error(err, fmt.Sprintf("source resource %s/%s/%s not found.", rule.Generation.Kind, rule.Generation.Clone.Namespace, rule.Generation.Clone.Name))
						continue
					}

					sourceObj, newResObj := stripNonPolicyFields(obj.Object, newRes.Object, logger)

					if _, err := gen.ValidateResourceWithPattern(logger, newResObj, sourceObj); err != nil {
						enqueueBool = true
						break
					}
				}
			}
		}
	}

	if enqueueBool {
		grName := resLabels["policy.kyverno.io/gr-name"]
		gr, err := ws.urLister.Get(grName)
		if err != nil {
			logger.Error(err, "failed to get generate request", "name", grName)
			return
		}
		ws.updateAnnotationInGR(gr, logger)
	}
}

func getGeneratedByResource(newRes *unstructured.Unstructured, resLabels map[string]string, client *client.Client, rule kyverno.Rule, logger logr.Logger) (kyverno.Rule, error) {
	var apiVersion, kind, name, namespace string
	sourceRequest := &admissionv1.AdmissionRequest{}
	kind = resLabels["kyverno.io/generated-by-kind"]
	name = resLabels["kyverno.io/generated-by-name"]
	if kind != "Namespace" {
		namespace = resLabels["kyverno.io/generated-by-namespace"]
	}
	obj, err := client.GetResource(apiVersion, kind, namespace, name)
	if err != nil {
		logger.Error(err, "source resource not found.")
		return rule, err
	}
	rawObj, err := json.Marshal(obj)
	if err != nil {
		logger.Error(err, "failed to marshal resource")
		return rule, err
	}
	sourceRequest.Object.Raw = rawObj
	sourceRequest.Operation = "CREATE"
	ctx := enginectx.NewContext()
	if err := ctx.AddRequest(sourceRequest); err != nil {
		logger.Error(err, "failed to load incoming request in context")
		return rule, err
	}
	if rule, err = variables.SubstituteAllInRule(logger, ctx, rule); err != nil {
		logger.Error(err, "variable substitution failed for rule %s", rule.Name)
		return rule, err
	}
	return rule, nil
}

//stripNonPolicyFields - remove feilds which get updated with each request by kyverno and are non policy fields
func stripNonPolicyFields(obj, newRes map[string]interface{}, logger logr.Logger) (map[string]interface{}, map[string]interface{}) {

	if metadata, found := obj["metadata"]; found {
		requiredMetadataInObj := make(map[string]interface{})
		if annotations, found := metadata.(map[string]interface{})["annotations"]; found {
			delete(annotations.(map[string]interface{}), "kubectl.kubernetes.io/last-applied-configuration")
			requiredMetadataInObj["annotations"] = annotations
		}

		if labels, found := metadata.(map[string]interface{})["labels"]; found {
			delete(labels.(map[string]interface{}), "generate.kyverno.io/clone-policy-name")
			requiredMetadataInObj["labels"] = labels
		}
		obj["metadata"] = requiredMetadataInObj
	}

	if metadata, found := newRes["metadata"]; found {
		requiredMetadataInNewRes := make(map[string]interface{})
		if annotations, found := metadata.(map[string]interface{})["annotations"]; found {
			requiredMetadataInNewRes["annotations"] = annotations
		}

		if labels, found := metadata.(map[string]interface{})["labels"]; found {
			requiredMetadataInNewRes["labels"] = labels
		}
		newRes["metadata"] = requiredMetadataInNewRes
	}

	if _, found := obj["status"]; found {
		delete(obj, "status")
	}

	if _, found := obj["spec"]; found {
		delete(obj["spec"].(map[string]interface{}), "tolerations")
	}

	if dataMap, found := obj["data"]; found {
		keyInData := make([]string, 0)
		switch dataMap.(type) {
		case map[string]interface{}:
			for k := range dataMap.(map[string]interface{}) {
				keyInData = append(keyInData, k)
			}
		}

		if len(keyInData) > 0 {
			for _, dataKey := range keyInData {
				originalResourceData := dataMap.(map[string]interface{})[dataKey]
				replaceData := strings.Replace(originalResourceData.(string), "\n", "", -1)
				dataMap.(map[string]interface{})[dataKey] = replaceData

				newResourceData := newRes["data"].(map[string]interface{})[dataKey]
				replacenewResourceData := strings.Replace(newResourceData.(string), "\n", "", -1)
				newRes["data"].(map[string]interface{})[dataKey] = replacenewResourceData
			}
		} else {
			logger.V(4).Info("data is not of type map[string]interface{}")
		}
	}

	return obj, newRes
}

//HandleDelete handles DELETE admission-requests for generate policies
func (ws *WebhookServer) handleDelete(request *admissionv1.AdmissionRequest) {
	logger := ws.log.WithValues("action", "generation", "uid", request.UID, "kind", request.Kind, "namespace", request.Namespace, "name", request.Name, "operation", request.Operation, "gvk", request.Kind.String())
	resource, err := enginutils.ConvertToUnstructured(request.OldObject.Raw)
	if err != nil {
		logger.Error(err, "failed to convert object resource to unstructured format")
	}

	resLabels := resource.GetLabels()
	if resLabels["app.kubernetes.io/managed-by"] == "kyverno" && request.Operation == admissionv1.Delete {
		grName := resLabels["policy.kyverno.io/gr-name"]
		gr, err := ws.urLister.Get(grName)
		if err != nil {
			logger.Error(err, "failed to get generate request", "name", grName)
			return
		}

		if gr.Spec.Type == urkyverno.Mutate {
			return
		}
		ws.updateAnnotationInGR(gr, logger)
	}
}

func (ws *WebhookServer) deleteGR(logger logr.Logger, engineResponse *response.EngineResponse) {
	logger.V(4).Info("querying all generate requests")
	selector := labels.SelectorFromSet(labels.Set(map[string]string{
		urkyverno.URGeneratePolicyLabel:          engineResponse.PolicyResponse.Policy.Name,
		"generate.kyverno.io/resource-name":      engineResponse.PolicyResponse.Resource.Name,
		"generate.kyverno.io/resource-kind":      engineResponse.PolicyResponse.Resource.Kind,
		"generate.kyverno.io/resource-namespace": engineResponse.PolicyResponse.Resource.Namespace,
	}))

	grList, err := ws.urLister.List(selector)
	if err != nil {
		logger.Error(err, "failed to get generate request for the resource", "kind", engineResponse.PolicyResponse.Resource.Kind, "name", engineResponse.PolicyResponse.Resource.Name, "namespace", engineResponse.PolicyResponse.Resource.Namespace)
		return
	}

	for _, v := range grList {
		err := ws.kyvernoClient.KyvernoV1beta1().UpdateRequests(config.KyvernoNamespace).Delete(contextdefault.TODO(), v.GetName(), metav1.DeleteOptions{})
		if err != nil {
			logger.Error(err, "failed to update gr")
		}
	}
}

func applyGenerateRequest(request *admissionv1.AdmissionRequest, ruleType urkyverno.RequestType, gnGenerator updaterequest.Interface, userRequestInfo urkyverno.RequestInfo,
	action admissionv1.Operation, engineResponses ...*response.EngineResponse) (failedGenerateRequest []generateRequestResponse) {

	requestBytes, err := json.Marshal(request)
	if err != nil {
		logger.Error(err, "error loading request into context")
	}
	admissionRequestInfo := urkyverno.AdmissionRequestInfoObject{
		AdmissionRequest: string(requestBytes),
		Operation:        action,
	}

	for _, er := range engineResponses {
		gr := transform(admissionRequestInfo, userRequestInfo, er, ruleType)
		if err := gnGenerator.Apply(gr, action); err != nil {
			failedGenerateRequest = append(failedGenerateRequest, generateRequestResponse{gr: gr, err: err})
		}
	}

	return
}

func transform(admissionRequestInfo urkyverno.AdmissionRequestInfoObject, userRequestInfo urkyverno.RequestInfo, er *response.EngineResponse, ruleType urkyverno.RequestType) urkyverno.UpdateRequestSpec {
	var PolicyNameNamespaceKey string
	if er.PolicyResponse.Policy.Namespace != "" {
		PolicyNameNamespaceKey = er.PolicyResponse.Policy.Namespace + "/" + er.PolicyResponse.Policy.Name
	} else {
		PolicyNameNamespaceKey = er.PolicyResponse.Policy.Name
	}

	gr := urkyverno.UpdateRequestSpec{
		Type:   ruleType,
		Policy: PolicyNameNamespaceKey,
		Resource: kyverno.ResourceSpec{
			Kind:       er.PolicyResponse.Resource.Kind,
			Namespace:  er.PolicyResponse.Resource.Namespace,
			Name:       er.PolicyResponse.Resource.Name,
			APIVersion: er.PolicyResponse.Resource.APIVersion,
		},
		Context: urkyverno.UpdateRequestSpecContext{
			UserRequestInfo:      userRequestInfo,
			AdmissionRequestInfo: admissionRequestInfo,
		},
	}

	return gr
}

type generateRequestResponse struct {
	gr  urkyverno.UpdateRequestSpec
	err error
}

func (resp generateRequestResponse) info() string {
	return strings.Join([]string{resp.gr.Resource.Kind, resp.gr.Resource.Namespace, resp.gr.Resource.Name}, "/")
}

func (resp generateRequestResponse) error() string {
	return resp.err.Error()
}

func failedEvents(err error, gr urkyverno.UpdateRequestSpec, resource unstructured.Unstructured) []event.Info {
	re := event.Info{}
	re.Kind = resource.GetKind()
	re.Namespace = resource.GetNamespace()
	re.Name = resource.GetName()
	re.Reason = event.PolicyFailed.String()
	re.Source = event.GeneratePolicyController
	re.Message = fmt.Sprintf("policy %s failed to apply: %v", gr.Policy, err)

	return []event.Info{re}
}
