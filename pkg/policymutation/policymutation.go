package policymutation

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/go-logr/logr"
	kyverno "github.com/kyverno/kyverno/api/kyverno/v1"
	"github.com/kyverno/kyverno/pkg/autogen"
	"github.com/kyverno/kyverno/pkg/toggle"
	jsonutils "github.com/kyverno/kyverno/pkg/utils/json"
	kubeutils "github.com/kyverno/kyverno/pkg/utils/kube"
)

// GenerateJSONPatchesForDefaults generates default JSON patches for
// - ValidationFailureAction
// - Background
// - auto-gen annotation and rules
func GenerateJSONPatchesForDefaults(policy kyverno.PolicyInterface, log logr.Logger) ([]byte, []string) {
	var patches [][]byte
	var updateMsgs []string
	spec := policy.GetSpec()
	// if autogenInternals is enabled, we don't mutate most of the policy fields
	if !toggle.AutogenInternals() {
		// default 'ValidationFailureAction'
		if patch, updateMsg := defaultvalidationFailureAction(spec, log); patch != nil {
			patches = append(patches, patch)
			updateMsgs = append(updateMsgs, updateMsg)
		}
		// default 'Background'
		if patch, updateMsg := defaultBackgroundFlag(spec, log); patch != nil {
			patches = append(patches, patch)
			updateMsgs = append(updateMsgs, updateMsg)
		}
		if patch, updateMsg := defaultFailurePolicy(spec, log); patch != nil {
			patches = append(patches, patch)
			updateMsgs = append(updateMsgs, updateMsg)
		}
		patch, errs := GeneratePodControllerRule(policy, log)
		if len(errs) > 0 {
			var errMsgs []string
			for _, err := range errs {
				errMsgs = append(errMsgs, err.Error())
				log.Error(err, "failed to generate pod controller rule")
			}
			updateMsgs = append(updateMsgs, strings.Join(errMsgs, ";"))
		}
		patches = append(patches, patch...)
	}
	formatedGVK, errs := checkForGVKFormatPatch(policy, log)
	if len(errs) > 0 {
		var errMsgs []string
		for _, err := range errs {
			errMsgs = append(errMsgs, err.Error())
			log.Error(err, "failed to format the kind")
		}
		updateMsgs = append(updateMsgs, strings.Join(errMsgs, ";"))
	}
	patches = append(patches, formatedGVK...)
	return jsonutils.JoinPatches(patches...), updateMsgs
}

func checkForGVKFormatPatch(policy kyverno.PolicyInterface, log logr.Logger) (patches [][]byte, errs []error) {
	patches = make([][]byte, 0)
	for i, rule := range autogen.ComputeRules(policy) {
		patchByte, err := convertGVKForKinds(fmt.Sprintf("/spec/rules/%s/match/resources/kinds", strconv.Itoa(i)), rule.MatchResources.Kinds, log)
		if err == nil && patchByte != nil {
			patches = append(patches, patchByte)
		} else if err != nil {
			errs = append(errs, fmt.Errorf("failed to GVK for rule '%s/%s/%d/match': %v", policy.GetName(), rule.Name, i, err))
		}

		for j, matchAll := range rule.MatchResources.All {
			patchByte, err := convertGVKForKinds(fmt.Sprintf("/spec/rules/%s/match/all/%s/resources/kinds", strconv.Itoa(i), strconv.Itoa(j)), matchAll.ResourceDescription.Kinds, log)
			if err == nil && patchByte != nil {
				patches = append(patches, patchByte)
			} else if err != nil {
				errs = append(errs, fmt.Errorf("failed to convert GVK for rule '%s/%s/%d/match/all/%d': %v", policy.GetName(), rule.Name, i, j, err))
			}
		}

		for k, matchAny := range rule.MatchResources.Any {
			patchByte, err := convertGVKForKinds(fmt.Sprintf("/spec/rules/%s/match/any/%s/resources/kinds", strconv.Itoa(i), strconv.Itoa(k)), matchAny.ResourceDescription.Kinds, log)
			if err == nil && patchByte != nil {
				patches = append(patches, patchByte)
			} else if err != nil {
				errs = append(errs, fmt.Errorf("failed to convert GVK for rule '%s/%s/%d/match/any/%d': %v", policy.GetName(), rule.Name, i, k, err))
			}
		}

		patchByte, err = convertGVKForKinds(fmt.Sprintf("/spec/rules/%s/exclude/resources/kinds", strconv.Itoa(i)), rule.ExcludeResources.Kinds, log)
		if err == nil && patchByte != nil {
			patches = append(patches, patchByte)
		} else if err != nil {
			errs = append(errs, fmt.Errorf("failed to convert GVK for rule '%s/%s/%d/exclude': %v", policy.GetName(), rule.Name, i, err))
		}

		for j, excludeAll := range rule.ExcludeResources.All {
			patchByte, err := convertGVKForKinds(fmt.Sprintf("/spec/rules/%s/exclude/all/%s/resources/kinds", strconv.Itoa(i), strconv.Itoa(j)), excludeAll.ResourceDescription.Kinds, log)
			if err == nil && patchByte != nil {
				patches = append(patches, patchByte)
			} else if err != nil {
				errs = append(errs, fmt.Errorf("failed to convert GVK for rule '%s/%s/%d/exclude/all/%d': %v", policy.GetName(), rule.Name, i, j, err))
			}
		}

		for k, excludeAny := range rule.ExcludeResources.Any {
			patchByte, err := convertGVKForKinds(fmt.Sprintf("/spec/rules/%s/exclude/any/%s/resources/kinds", strconv.Itoa(i), strconv.Itoa(k)), excludeAny.ResourceDescription.Kinds, log)
			if err == nil && patchByte != nil {
				patches = append(patches, patchByte)
			} else if err != nil {
				errs = append(errs, fmt.Errorf("failed to convert GVK for rule '%s/%s/%d/exclude/any/%d': %v", policy.GetName(), rule.Name, i, k, err))
			}
		}
	}

	return patches, errs
}

func convertGVKForKinds(path string, kinds []string, log logr.Logger) ([]byte, error) {
	kindList := []string{}
	for _, k := range kinds {
		gvk := kubeutils.GetFormatedKind(k)
		if gvk == k {
			continue
		}
		kindList = append(kindList, gvk)
	}
	if len(kindList) == 0 {
		return nil, nil
	}
	p, err := jsonutils.MarshalPatch(path, "replace", kindList)
	log.V(4).WithName("convertGVKForKinds").Info("generated patch", "patch", string(p))
	return p, err
}

func defaultBackgroundFlag(spec *kyverno.Spec, log logr.Logger) ([]byte, string) {
	// set 'Background' flag to 'true' if not specified
	if spec.Background == nil {
		defaultVal := true
		log.V(4).Info("setting default value", "spec.background", true)
		patchByte, err := jsonutils.MarshalPatch("/spec/background", "add", &defaultVal)
		if err != nil {
			log.Error(err, "failed to set default value", "spec.background", true)
			return nil, ""
		}
		log.V(3).Info("generated JSON Patch to set default", "spec.background", true)
		return patchByte, fmt.Sprintf("default 'Background' to '%s'", strconv.FormatBool(true))
	}
	return nil, ""
}

func defaultvalidationFailureAction(spec *kyverno.Spec, log logr.Logger) ([]byte, string) {
	// set ValidationFailureAction to "audit" if not specified
	if spec.ValidationFailureAction == "" {
		audit := kyverno.Audit
		log.V(4).Info("setting default value", "spec.validationFailureAction", audit)
		patchByte, err := jsonutils.MarshalPatch("/spec/validationFailureAction", "add", audit)
		if err != nil {
			log.Error(err, "failed to default value", "spec.validationFailureAction", audit)
			return nil, ""
		}
		log.V(3).Info("generated JSON Patch to set default", "spec.validationFailureAction", audit)
		return patchByte, fmt.Sprintf("default 'ValidationFailureAction' to '%s'", audit)
	}
	return nil, ""
}

func defaultFailurePolicy(spec *kyverno.Spec, log logr.Logger) ([]byte, string) {
	// set failurePolicy to Fail if not present
	if spec.FailurePolicy == nil {
		failurePolicy := string(kyverno.Fail)
		log.V(4).Info("setting default value", "spec.failurePolicy", failurePolicy)
		patchByte, err := jsonutils.MarshalPatch("/spec/failurePolicy", "add", failurePolicy)
		if err != nil {
			log.Error(err, "failed to set default value", "spec.failurePolicy", failurePolicy)
			return nil, ""
		}
		log.V(3).Info("generated JSON Patch to set default", "spec.failurePolicy", failurePolicy)
		return patchByte, fmt.Sprintf("default failurePolicy to '%s'", failurePolicy)
	}
	return nil, ""
}

// podControllersKey annotation could be:
// scenario A: not exist, set default to "all", which generates on all pod controllers
//               - if name / selector exist in resource description -> skip
//                 as these fields may not be applicable to pod controllers
// scenario B: "none", user explicitly disable this feature -> skip
// scenario C: some certain controllers that user set -> generate on defined controllers
//             copy entire match / exclude block, it's users' responsibility to
//             make sure all fields are applicable to pod controllers

// GeneratePodControllerRule returns two patches: rulePatches and annotation patch(if necessary)
func GeneratePodControllerRule(policy kyverno.PolicyInterface, log logr.Logger) (patches [][]byte, errs []error) {
	spec := policy.GetSpec()
	applyAutoGen, desiredControllers := autogen.CanAutoGen(spec)

	if !applyAutoGen {
		desiredControllers = "none"
	}

	ann := policy.GetAnnotations()
	actualControllers, ok := ann[kyverno.PodControllersAnnotation]

	// - scenario A
	// - predefined controllers are invalid, overwrite the value
	if !ok || !applyAutoGen {
		actualControllers = desiredControllers
		annPatch, err := defaultPodControllerAnnotation(ann, actualControllers)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to generate pod controller annotation for policy '%s': %v", policy.GetName(), err))
		} else {
			patches = append(patches, annPatch)
		}
	} else {
		if !applyAutoGen {
			actualControllers = desiredControllers
		}
	}

	// scenario B
	if actualControllers == "none" {
		return patches, nil
	}

	log.V(3).Info("auto generating rule for pod controllers", "controllers", actualControllers)

	p, err := autogen.GenerateRulePatches(spec, actualControllers)
	patches = append(patches, p...)
	errs = append(errs, err...)
	return
}

// defaultPodControllerAnnotation inserts an annotation
// "pod-policies.kyverno.io/autogen-controllers=<controllers>" to policy
func defaultPodControllerAnnotation(ann map[string]string, controllers string) ([]byte, error) {
	if ann == nil {
		ann = make(map[string]string)
		ann[kyverno.PodControllersAnnotation] = controllers
		patchByte, err := jsonutils.MarshalPatch("/metadata/annotations", "add", ann)
		if err != nil {
			return nil, err
		}
		return patchByte, nil
	}
	patchByte, err := jsonutils.MarshalPatch("/metadata/annotations/pod-policies.kyverno.io~1autogen-controllers", "add", controllers)
	if err != nil {
		return nil, err
	}
	return patchByte, nil
}
