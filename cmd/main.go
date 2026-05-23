// Command secret-distribution-operator runs the ExportSecret and
// ImportSecret reconcilers under a single controller-runtime manager.
package main

import (
	"flag"
	"os"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	secretsv1alpha1 "github.com/example/secret-distribution-operator/api/v1alpha1"
	"github.com/example/secret-distribution-operator/internal/controller"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(secretsv1alpha1.AddToScheme(scheme))
}

func main() {
	var (
		metricsAddr          string
		probeAddr            string
		enableLeaderElection bool
		awsRegion            string
	)
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", true,
		"Enable leader election for controller manager.")
	flag.StringVar(&awsRegion, "aws-region", os.Getenv("AWS_REGION"),
		"AWS region to use for Secrets Manager. Defaults to $AWS_REGION.")
	opts := zap.Options{Development: false}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	setupLog := ctrl.Log.WithName("setup")

	// SetupSignalHandler must be called exactly once; the returned context is
	// passed to both the SDK config loader and the manager's Start.
	signalCtx := ctrl.SetupSignalHandler()

	// IRSA-backed AWS config; the SDK picks up
	// AWS_ROLE_ARN + AWS_WEB_IDENTITY_TOKEN_FILE injected by the EKS
	// pod-identity webhook.
	awsCfg, err := config.LoadDefaultConfig(signalCtx, config.WithRegion(awsRegion))
	if err != nil {
		setupLog.Error(err, "unable to load AWS config")
		os.Exit(1)
	}
	smClient := secretsmanager.NewFromConfig(awsCfg)
	smWrapper := controller.NewSecretsManagerWrapper(smClient)

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "secret-distribution-operator.platform.mtkp",
	})
	if err != nil {
		setupLog.Error(err, "unable to create manager")
		os.Exit(1)
	}

	if err := (&controller.ExportSecretReconciler{
		Client:         mgr.GetClient(),
		Scheme:         mgr.GetScheme(),
		SecretsManager: smWrapper,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ExportSecret")
		os.Exit(1)
	}

	if err := (&controller.ImportSecretReconciler{
		Client:         mgr.GetClient(),
		Scheme:         mgr.GetScheme(),
		SecretsManager: smWrapper,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ImportSecret")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(signalCtx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
