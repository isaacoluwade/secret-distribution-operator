// Package metrics declares the operator's custom Prometheus metrics. They
// are registered into the controller-runtime metrics.Registry so the manager
// exposes them on its standard :8080/metrics endpoint without extra wiring.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// ReconcileDuration tracks per-reconcile latency in seconds, labeled by
	// the CRD kind ("ExportSecret" | "ImportSecret") and outcome
	// ("success" | "error" | "policy_denied").
	ReconcileDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "secret_distribution_reconcile_duration_seconds",
			Help:    "Time spent in each Reconcile call.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"kind", "outcome"},
	)

	// PolicyViolationsTotal counts every policy-denied reconcile, labeled by
	// kind and the specific reason ("bad_path" | "namespace_mismatch" |
	// "bad_arn").
	PolicyViolationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "secret_distribution_policy_violations_total",
			Help: "Total reconciles that were rejected by platform policy.",
		},
		[]string{"kind", "reason"},
	)

	// AWSAPIErrorsTotal counts every error returned from the AWS Secrets
	// Manager API, labeled by the SDK operation name and a short error class
	// ("not_found" | "access_denied" | "throttled" | "other").
	AWSAPIErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "secret_distribution_aws_api_errors_total",
			Help: "Total errors returned from AWS Secrets Manager.",
		},
		[]string{"operation", "class"},
	)
)

func init() {
	ctrlmetrics.Registry.MustRegister(
		ReconcileDuration,
		PolicyViolationsTotal,
		AWSAPIErrorsTotal,
	)
}
