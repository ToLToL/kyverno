package event

//Source of event generation
type Source int

const (
	// AdmissionController : event generated in admission-control webhook
	AdmissionController Source = iota
	// PolicyController : event generated in policy-controller
	PolicyController
	// GeneratePolicyController : event generated in generate policyController
	GeneratePolicyController
	// MutateExistingController : event generated for mutateExisting policies
	MutateExistingController
)

func (s Source) String() string {
	return [...]string{
		"admission-controller",
		"policy-controller",
		"generate-policy-controller",
		"mutate-existing-controller",
	}[s]
}
