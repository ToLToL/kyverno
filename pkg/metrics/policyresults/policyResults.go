package policyresults

import (
	"fmt"

	kyverno "github.com/kyverno/kyverno/api/kyverno/v1"
	"github.com/kyverno/kyverno/pkg/engine/response"
	"github.com/kyverno/kyverno/pkg/metrics"
	"github.com/kyverno/kyverno/pkg/utils"
	prom "github.com/prometheus/client_golang/prometheus"
)

func registerPolicyResultsMetric(
	pc *metrics.PromConfig,
	policyValidationMode metrics.PolicyValidationMode,
	policyType metrics.PolicyType,
	policyBackgroundMode metrics.PolicyBackgroundMode,
	policyNamespace, policyName string,
	resourceKind, resourceNamespace string,
	resourceRequestOperation metrics.ResourceRequestOperation,
	ruleName string,
	ruleResult metrics.RuleResult,
	ruleType metrics.RuleType,
	ruleExecutionCause metrics.RuleExecutionCause,
) error {
	if policyType == metrics.Cluster {
		policyNamespace = "-"
	}
	includeNamespaces, excludeNamespaces := pc.Config.GetIncludeNamespaces(), pc.Config.GetExcludeNamespaces()
	if (resourceNamespace != "" && resourceNamespace != "-") && utils.ContainsString(excludeNamespaces, resourceNamespace) {
		pc.Log.Info(fmt.Sprintf("Skipping the registration of kyverno_policy_results_total metric as the operation belongs to the namespace '%s' which is one of 'namespaces.exclude' %+v in values.yaml", resourceNamespace, excludeNamespaces))
		return nil
	}
	if (resourceNamespace != "" && resourceNamespace != "-") && len(includeNamespaces) > 0 && !utils.ContainsString(includeNamespaces, resourceNamespace) {
		pc.Log.Info(fmt.Sprintf("Skipping the registration of kyverno_policy_results_total metric as the operation belongs to the namespace '%s' which is not one of 'namespaces.include' %+v in values.yaml", resourceNamespace, includeNamespaces))
		return nil
	}
	pc.Metrics.PolicyResults.With(prom.Labels{
		"policy_validation_mode":     string(policyValidationMode),
		"policy_type":                string(policyType),
		"policy_background_mode":     string(policyBackgroundMode),
		"policy_namespace":           policyNamespace,
		"policy_name":                policyName,
		"resource_kind":              resourceKind,
		"resource_namespace":         resourceNamespace,
		"resource_request_operation": string(resourceRequestOperation),
		"rule_name":                  ruleName,
		"rule_result":                string(ruleResult),
		"rule_type":                  string(ruleType),
		"rule_execution_cause":       string(ruleExecutionCause),
	}).Inc()
	return nil
}

//policy - policy related data
//engineResponse - resource and rule related data
func ProcessEngineResponse(pc *metrics.PromConfig, policy kyverno.PolicyInterface, engineResponse response.EngineResponse, executionCause metrics.RuleExecutionCause, resourceRequestOperation metrics.ResourceRequestOperation) error {
	name, namespace, policyType, backgroundMode, validationMode, err := metrics.GetPolicyInfos(policy)
	if err != nil {
		return err
	}
	resourceSpec := engineResponse.PolicyResponse.Resource
	resourceKind := resourceSpec.Kind
	resourceNamespace := resourceSpec.Namespace
	ruleResponses := engineResponse.PolicyResponse.Rules
	for _, rule := range ruleResponses {
		ruleName := rule.Name
		ruleType := metrics.ParseRuleTypeFromEngineRuleResponse(rule)
		var ruleResult metrics.RuleResult
		switch rule.Status {
		case response.RuleStatusPass:
			ruleResult = metrics.Pass
		case response.RuleStatusFail:
			ruleResult = metrics.Fail
		case response.RuleStatusWarn:
			ruleResult = metrics.Warn
		case response.RuleStatusError:
			ruleResult = metrics.Error
		case response.RuleStatusSkip:
			ruleResult = metrics.Skip
		default:
			ruleResult = metrics.Fail
		}
		if err := registerPolicyResultsMetric(
			pc,
			validationMode,
			policyType,
			backgroundMode,
			namespace, name,
			resourceKind, resourceNamespace,
			resourceRequestOperation,
			ruleName,
			ruleResult,
			ruleType,
			executionCause,
		); err != nil {
			return err
		}
	}
	return nil
}
