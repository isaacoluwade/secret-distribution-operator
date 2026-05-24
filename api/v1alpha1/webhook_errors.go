// Helpers shared between the ExportSecret and ImportSecret validators for
// producing well-formed admission rejection responses. Kept in its own file
// so the two webhook files can stay focused on their per-CRD rule logic.

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// fieldErrorsFromCauses turns each plain-string cause into a field.Error
// rooted at spec. The webhook framework wraps these into a StatusError so
// kubectl will render them as:
//
//	The ExportSecret "X" is invalid: spec: Invalid value: <cause>
//
// We deliberately keep the path coarse (just `spec`) because each cause
// message already names its sub-field — duplicating that in field.Path
// would just make output noisier.
func fieldErrorsFromCauses(causes []string) field.ErrorList {
	errs := make(field.ErrorList, 0, len(causes))
	for _, c := range causes {
		errs = append(errs, field.Invalid(field.NewPath("spec"), "", c))
	}
	return errs
}
