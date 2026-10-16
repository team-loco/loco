package controller

import (
	"context"
	"errors"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	v1Gateway "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/team-loco/loco/controller/internal/managed"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	transitionProduction  = "01902cec-9b2d-7000-8000-000000000001"
	transitionQA          = "01902cec-9b2d-7000-8000-000000000002"
	transitionTestTimeout = 10 * time.Second
)

func initialEnvironmentFixture(name string) (*locov1alpha1.Application, *LocoResourceReconciler, *appsv1.Deployment) {
	Expect(v1Gateway.Install(k8sClient.Scheme())).To(Succeed())
	app := isolationTestApplication(name, name)
	app.Spec.EnvironmentID = transitionProduction
	app.Annotations = map[string]string{
		locov1alpha1.AnnotationPlacementID:       name,
		locov1alpha1.AnnotationPlacementRevision: "1",
	}
	app.Spec.ServiceSpec.Routing = nil
	stage := &corev1.Secret{
		Name: name + "-staged", Namespace: testNamespace,
		Labels: map[string]string{locov1alpha1.LabelPlacementRevision: "1"},
		Data:   map[string][]byte{"VALUE": []byte("production")},
	}
	Expect(k8sClient.Create(ctx, stage)).To(Succeed())
	app.Spec.ServiceSpec.Deployment.EnvSecretRef = &locov1alpha1.EnvSecretRef{Name: stage.Name, Revision: 1}
	Expect(k8sClient.Create(ctx, app)).To(Succeed())
	r := &LocoResourceReconciler{
		Client: k8sClient, Scheme: k8sClient.Scheme(), APIReader: k8sClient,
		LocoNamespace: testNamespace, Telemetry: testTelemetry(),
	}
	req := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(app)}
	_, err := r.Reconcile(ctx, req)
	Expect(err).NotTo(HaveOccurred())
	dep := &appsv1.Deployment{}
	key := client.ObjectKey{Namespace: getNamespace(app), Name: getName(app)}
	Expect(k8sClient.Get(ctx, key, dep)).To(Succeed())
	Expect(dep.Labels[labelEnvironmentID]).To(Equal(transitionProduction))
	DeferCleanup(func() {
		Expect(k8sClient.Delete(ctx, stage)).To(Succeed())
		Expect(k8sClient.Get(ctx, req.NamespacedName, app)).To(Succeed())
		Expect(k8sClient.Delete(ctx, app)).To(Succeed())
		_, cleanupErr := r.Reconcile(ctx, req)
		Expect(cleanupErr).NotTo(HaveOccurred())
	})
	Expect(k8sClient.Get(ctx, req.NamespacedName, app)).To(Succeed())
	return app, r, dep
}

func finishIdentityRollout(app *locov1alpha1.Application, r *LocoResourceReconciler) {
	dep := &appsv1.Deployment{}
	Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: getNamespace(app), Name: getName(app)}, dep)).To(Succeed())
	dep.Status = appsv1.DeploymentStatus{
		ObservedGeneration: dep.Generation,
		Replicas:           *dep.Spec.Replicas,
		ReadyReplicas:      *dep.Spec.Replicas,
		AvailableReplicas:  *dep.Spec.Replicas,
		UpdatedReplicas:    *dep.Spec.Replicas,
	}
	Expect(k8sClient.Status().Update(ctx, dep)).To(Succeed())
	_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(app)})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(app), app)).To(Succeed())
	Expect(app.Status.Phase).To(Equal(phaseReady))
}

func retargetApplication(app *locov1alpha1.Application) {
	stage := &corev1.Secret{}
	key := client.ObjectKey{Namespace: app.Namespace, Name: app.Spec.ServiceSpec.Deployment.EnvSecretRef.Name}
	Expect(k8sClient.Get(ctx, key, stage)).To(Succeed())
	stage.Data["VALUE"] = []byte("qa")
	stage.Labels[locov1alpha1.LabelPlacementRevision] = "3"
	Expect(k8sClient.Update(ctx, stage)).To(Succeed())
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(app), app)).To(Succeed())
	app.Spec.EnvironmentID = transitionQA
	app.Annotations[locov1alpha1.AnnotationPlacementRevision] = "3"
	app.Spec.ServiceSpec.Deployment.EnvSecretRef.Revision = 3
	Expect(k8sClient.Update(ctx, app)).To(Succeed())
}

func retargetFixture(name string) (*locov1alpha1.Application, *LocoResourceReconciler, *appsv1.Deployment) {
	app, r, dep := initialEnvironmentFixture(name)
	retargetApplication(app)
	return app, r, dep
}

type pausedSecretClient struct {
	client.Client
	namespace string
	name      string
	paused    chan struct{}
	release   chan struct{}
	once      sync.Once
}

func (c *pausedSecretClient) Get(
	ctx context.Context,
	key client.ObjectKey,
	obj client.Object,
	opts ...client.GetOption,
) error {
	err := c.Client.Get(ctx, key, obj, opts...)
	if _, ok := obj.(*corev1.Secret); ok && key.Namespace == c.namespace && key.Name == c.name {
		c.once.Do(func() {
			close(c.paused)
			<-c.release
		})
	}
	return err
}

type deploymentReplacementClient struct {
	client.Client
	replace   func()
	deleteErr error
}

func (c *deploymentReplacementClient) Delete(
	ctx context.Context,
	obj client.Object,
	opts ...client.DeleteOption,
) error {
	if _, ok := obj.(*appsv1.Deployment); ok && c.replace != nil {
		replace := c.replace
		c.replace = nil
		replace()
	}
	c.deleteErr = c.Client.Delete(ctx, obj, opts...)
	return c.deleteErr
}

type deploymentCreationClient struct {
	client.Client
	beforeCreate func()
}

func (c *deploymentCreationClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if _, ok := obj.(*appsv1.Deployment); ok && c.beforeCreate != nil {
		before := c.beforeCreate
		c.beforeCreate = nil
		before()
	}
	return c.Client.Create(ctx, obj, opts...)
}

var errInterruptedDeployment = errors.New("interrupted before full deployment apply")

type interruptedDeploymentClient struct{ client.Client }

func (c *interruptedDeploymentClient) Apply(
	ctx context.Context,
	obj runtime.ApplyConfiguration,
	opts ...client.ApplyOption,
) error {
	if _, ok := obj.(*appsv1ac.DeploymentApplyConfiguration); ok {
		return errInterruptedDeployment
	}
	return c.Client.Apply(ctx, obj, opts...)
}

type deploymentStatusRaceClient struct {
	client.Client
	injected bool
}

func (c *deploymentStatusRaceClient) Apply(
	ctx context.Context,
	obj runtime.ApplyConfiguration,
	opts ...client.ApplyOption,
) error {
	if dep, ok := obj.(*appsv1ac.DeploymentApplyConfiguration); ok && !c.injected {
		c.injected = true
		current := &appsv1.Deployment{}
		if err := c.Get(ctx, client.ObjectKey{Namespace: *dep.Namespace, Name: *dep.Name}, current); err != nil {
			return err
		}
		current.Status.Replicas = 1
		current.Status.ReadyReplicas = 1
		if err := c.Client.Status().Update(ctx, current); err != nil {
			return err
		}
	}
	return c.Client.Apply(ctx, obj, opts...)
}

var _ = Describe("Deployment environment identity", func() {
	It("preserves the old Deployment environment while its replicas stop", func() {
		app, r, original := retargetFixture("identity-deployment")
		req := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(app)}
		_, err := r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		current := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(original), current)).To(Succeed())
		Expect(current.UID).To(Equal(original.UID))
		Expect(current.Labels[labelEnvironmentID]).To(Equal(transitionProduction))
		Expect(current.DeletionTimestamp.IsZero()).To(BeFalse())
	})
	It("keeps the old environment secret until its Deployment is gone", func() {
		app, r, original := retargetFixture("identity-secret")
		req := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(app)}
		_, err := r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		secret := &corev1.Secret{}
		key := client.ObjectKey{
			Namespace: getNamespace(app),
			Name:      original.Spec.Template.Spec.Containers[0].EnvFrom[0].SecretRef.Name,
		}
		Expect(k8sClient.Get(ctx, key, secret)).To(Succeed())
		Expect(string(secret.Data["VALUE"])).To(Equal("production"))
	})
	It("creates a fresh Deployment after foreground cleanup without recreating the Application", func() {
		app, r, original := retargetFixture("identity-complete")
		appUID := app.UID
		req := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(app)}
		result, err := r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(deployingRequeue))
		current := &appsv1.Deployment{}
		key := client.ObjectKeyFromObject(original)
		Expect(k8sClient.Get(ctx, key, current)).To(Succeed())
		Expect(current.Finalizers).To(ContainElement(metav1.FinalizerDeleteDependents))
		Expect(k8sClient.Get(ctx, req.NamespacedName, app)).To(Succeed())
		Expect(app.Status.Phase).To(Equal(phaseDeploying))
		Expect(app.Status.ObservedPlacementRevision).To(Equal(int64(3)))
		Expect(app.Status.ReadyReplicas).To(BeZero())
		current.Finalizers = nil
		Expect(k8sClient.Update(ctx, current)).To(Succeed())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key, current))).To(BeTrue())
		_, err = r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, key, current)).To(Succeed())
		Expect(current.UID).NotTo(Equal(original.UID))
		Expect(current.Labels[labelEnvironmentID]).To(Equal(transitionQA))
		Expect(k8sClient.Get(ctx, req.NamespacedName, app)).To(Succeed())
		Expect(app.UID).To(Equal(appUID))
		secret := &corev1.Secret{}
		Expect(
			k8sClient.Get(ctx, client.ObjectKey{Namespace: key.Namespace, Name: getEnvSecretName(app)}, secret),
		).To(Succeed())
		Expect(string(secret.Data["VALUE"])).To(Equal("qa"))
	})
	It("keeps the Deployment UID for a same-environment secret rotation", func() {
		app, r, original := retargetFixture("identity-rotation")
		app.Spec.EnvironmentID = transitionProduction
		Expect(k8sClient.Update(ctx, app)).To(Succeed())
		req := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(app)}
		_, err := r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		current := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(original), current)).To(Succeed())
		Expect(current.UID).To(Equal(original.UID))
		Expect(current.DeletionTimestamp.IsZero()).To(BeTrue())
		secret := &corev1.Secret{}
		Expect(
			k8sClient.Get(ctx, client.ObjectKey{Namespace: current.Namespace, Name: getEnvSecretName(app)}, secret),
		).To(Succeed())
		Expect(string(secret.Data["VALUE"])).To(Equal("qa"))
	})
	It("does not retire the replacement when a reconcile carries the old environment", func() {
		app, r, original := retargetFixture("identity-stale")
		Expect(k8sClient.Delete(ctx, original)).To(Succeed())
		replacement, err := desiredDeployment(app, envSecretMount{}, testTelemetry())
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Apply(ctx, replacement, managed.ApplyOptions()...)).To(Succeed())
		stale := app.DeepCopy()
		stale.Spec.EnvironmentID = transitionProduction
		_, pending, err := r.retirePreviousEnvironmentDeployment(ctx, stale)
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(BeTrue())
		current := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(original), current)).To(Succeed())
		Expect(current.DeletionTimestamp.IsZero()).To(BeTrue())
		Expect(current.Labels[labelEnvironmentID]).To(Equal(transitionQA))
	})
	It("does not delete a replacement created between the read and foreground delete", func() {
		app, r, original := retargetFixture("identity-delete-race")
		racing := &deploymentReplacementClient{Client: k8sClient}
		racing.replace = func() {
			Expect(k8sClient.Delete(ctx, original)).To(Succeed())
			replacement, err := desiredDeployment(app, envSecretMount{}, testTelemetry())
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Apply(ctx, replacement, managed.ApplyOptions()...)).To(Succeed())
		}
		r.Client = racing
		_, pending, err := r.retirePreviousEnvironmentDeployment(ctx, app)
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(BeTrue())
		Expect(apierrors.IsConflict(racing.deleteErr)).To(BeTrue())
		current := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(original), current)).To(Succeed())
		Expect(current.UID).NotTo(Equal(original.UID))
		Expect(current.DeletionTimestamp.IsZero()).To(BeTrue())
	})

	It("keeps foreign Deployment ownership errors terminal", func() {
		app, r, original := initialEnvironmentFixture("identity-foreign")
		original.Annotations[annotationAppName] = "another-application"
		Expect(k8sClient.Update(ctx, original)).To(Succeed())
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(app)})
		Expect(errors.Is(err, errForeignEnvironmentDeployment)).To(BeTrue())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(app), app)).To(Succeed())
		Expect(app.Status.Phase).To(Equal(phaseFailed))
	})
	It("requeues an actual status-write conflict without marking the rollout failed", func() {
		app, r, _ := initialEnvironmentFixture("identity-status-race")
		r.Client = &deploymentStatusRaceClient{Client: k8sClient}
		result, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(app)})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(deployingRequeue))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(app), app)).To(Succeed())
		Expect(app.Status.Phase).To(Equal(phaseDeploying))
	})

	It("keeps an interrupted bootstrap at zero replicas and finishes it on the next reconcile", func() {
		app, r, original := initialEnvironmentFixture("identity-bootstrap")
		Expect(k8sClient.Delete(ctx, original)).To(Succeed())
		fresh := *r
		r.Client = &interruptedDeploymentClient{Client: k8sClient}
		_, err := r.ensureDeployment(ctx, app, envSecretMount{}, nil)
		Expect(errors.Is(err, errInterruptedDeployment)).To(BeTrue())
		bootstrap := &appsv1.Deployment{}
		key := client.ObjectKeyFromObject(original)
		Expect(k8sClient.Get(ctx, key, bootstrap)).To(Succeed())
		Expect(*bootstrap.Spec.Replicas).To(BeZero())
		Expect(bootstrap.Spec.Template.Spec.Containers[0].Env).To(BeEmpty())
		bootstrapUID := bootstrap.UID
		_, err = fresh.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(app)})
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, key, bootstrap)).To(Succeed())
		Expect(bootstrap.UID).To(Equal(bootstrapUID))
		Expect(*bootstrap.Spec.Replicas).To(Equal(app.Spec.ServiceSpec.Resources.Replicas.Min))
		Expect(bootstrap.Spec.Template.Spec.Containers[0].EnvFrom[0].SecretRef.Name).To(Equal(getEnvSecretName(app)))
		Expect(bootstrap.Spec.Template.Spec.SecurityContext.RunAsNonRoot).To(HaveValue(BeTrue()))
	})

	It("does not upsert over a Deployment created after the initial missing read", func() {
		app, r, original := initialEnvironmentFixture("identity-create-race")
		Expect(k8sClient.Delete(ctx, original)).To(Succeed())
		replacement := original.DeepCopy()
		replacement.UID, replacement.ResourceVersion = "", ""
		replacement.ManagedFields = nil
		replacement.Labels[labelEnvironmentID] = transitionQA
		replacement.Spec.Selector.MatchLabels[labelEnvironmentID] = transitionQA
		replacement.Spec.Template.Labels[labelEnvironmentID] = transitionQA
		r.Client = &deploymentCreationClient{Client: k8sClient, beforeCreate: func() {
			Expect(k8sClient.Create(ctx, replacement)).To(Succeed())
		}}
		_, err := r.ensureDeployment(ctx, app, envSecretMount{}, nil)
		Expect(apierrors.IsAlreadyExists(err)).To(BeTrue())
		current := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(original), current)).To(Succeed())
		Expect(current.UID).To(Equal(replacement.UID))
		Expect(current.Labels[labelEnvironmentID]).To(Equal(transitionQA))
	})

	DescribeTable("uses the target Secret resource version when an old reconcile resumes",
		func(remove bool, name string) {
			app, old, _ := initialEnvironmentFixture(name)
			fresh := *old
			stage := &corev1.Secret{}
			stageKey := client.ObjectKey{
				Namespace: app.Namespace,
				Name:      app.Spec.ServiceSpec.Deployment.EnvSecretRef.Name,
			}
			Expect(k8sClient.Get(ctx, stageKey, stage)).To(Succeed())
			stage.Data["VALUE"] = []byte("production-pending")
			stage.Labels[locov1alpha1.LabelPlacementRevision] = "2"
			Expect(k8sClient.Update(ctx, stage)).To(Succeed())
			app.Spec.ServiceSpec.Deployment.EnvSecretRef.Revision = 2
			if remove {
				app.Spec.ServiceSpec.Deployment.EnvSecretRef = nil
			}
			Expect(k8sClient.Update(ctx, app)).To(Succeed())
			stale := app.DeepCopy()
			blocked := &pausedSecretClient{
				Client: k8sClient, namespace: getNamespace(app), name: getEnvSecretName(app),
				paused: make(chan struct{}), release: make(chan struct{}),
			}
			old.APIReader = blocked
			done := make(chan struct{})
			var oldErr error
			resume := sync.OnceFunc(func() { close(blocked.release) })
			DeferCleanup(func() {
				resume()
				Eventually(done, transitionTestTimeout).Should(BeClosed())
			})
			go func() {
				defer close(done)
				_, oldErr = old.ensureEnvSecret(ctx, stale)
			}()
			Eventually(blocked.paused, transitionTestTimeout).Should(BeClosed())
			Expect(k8sClient.Get(ctx, stageKey, stage)).To(Succeed())
			stage.Data["VALUE"] = []byte("production-new")
			stage.Labels[locov1alpha1.LabelPlacementRevision] = "3"
			Expect(k8sClient.Update(ctx, stage)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(app), app)).To(Succeed())
			app.Spec.ServiceSpec.Deployment.EnvSecretRef = &locov1alpha1.EnvSecretRef{Name: stage.Name, Revision: 3}
			Expect(k8sClient.Update(ctx, app)).To(Succeed())
			_, err := fresh.ensureEnvSecret(ctx, app)
			Expect(err).NotTo(HaveOccurred())
			resume()
			Eventually(done, transitionTestTimeout).Should(BeClosed())
			Expect(apierrors.IsConflict(oldErr)).To(BeTrue())
			current := &corev1.Secret{}
			Expect(
				k8sClient.Get(
					ctx,
					client.ObjectKey{Namespace: getNamespace(app), Name: getEnvSecretName(app)},
					current,
				),
			).To(Succeed())
			Expect(string(current.Data["VALUE"])).To(Equal("production-new"))
		},
		Entry("stale data update", false, "identity-secret-cas-write"),
		Entry("stale nil-ref delete", true, "identity-secret-cas-delete"),
	)

	It("rejects a stale same-environment snapshot before deleting a newer secret", func() {
		app, r, original := initialEnvironmentFixture("identity-stale-generation")
		stale := app.DeepCopy()
		stale.Spec.ServiceSpec.Deployment.EnvSecretRef = nil
		retargetApplication(app)
		app.Spec.EnvironmentID = transitionProduction
		Expect(k8sClient.Update(ctx, app)).To(Succeed())
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(app)})
		Expect(err).NotTo(HaveOccurred())
		result, err := r.reconcileResources(ctx, stale)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(deployingRequeue))
		current := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(original), current)).To(Succeed())
		Expect(current.UID).To(Equal(original.UID))
		Expect(current.Spec.Template.Annotations[annotationEnvSecretRevision]).To(Equal("3"))
		secret := &corev1.Secret{}
		Expect(
			k8sClient.Get(ctx, client.ObjectKey{Namespace: current.Namespace, Name: getEnvSecretName(app)}, secret),
		).To(Succeed())
		Expect(string(secret.Data["VALUE"])).To(Equal("qa"))
	})

	It("recreates an existing Deployment with the legacy environment-free selector", func() {
		app, r, original := initialEnvironmentFixture("identity-legacy-selector")
		Expect(k8sClient.Delete(ctx, original)).To(Succeed())
		legacy, err := desiredDeployment(app, envSecretMount{}, testTelemetry())
		Expect(err).NotTo(HaveOccurred())
		delete(legacy.Spec.Selector.MatchLabels, labelEnvironmentID)
		Expect(k8sClient.Apply(ctx, legacy, managed.ApplyOptions()...)).To(Succeed())
		_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(app)})
		Expect(err).NotTo(HaveOccurred())
		current := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(original), current)).To(Succeed())
		Expect(current.DeletionTimestamp.IsZero()).To(BeFalse())
		Expect(current.Finalizers).To(ContainElement(metav1.FinalizerDeleteDependents))
	})

	It("cleans late old-generation Secrets without deleting active, foreign, or newer copies", func() {
		app, r, original := retargetFixture("identity-secret-cleanup")
		req := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(app)}
		_, err := r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		retiring := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(original), retiring)).To(Succeed())
		retiring.Finalizers = nil
		Expect(k8sClient.Update(ctx, retiring)).To(Succeed())
		_, err = r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		active := &corev1.Secret{}
		activeKey := client.ObjectKey{Namespace: getNamespace(app), Name: getEnvSecretName(app)}
		Expect(k8sClient.Get(ctx, activeKey, active)).To(Succeed())
		late := active.DeepCopy()
		late.Name += "-obsolete"
		late.ResourceVersion, late.UID = "", ""
		late.Annotations[annotationApplicationGeneration] = "1"
		Expect(k8sClient.Create(ctx, late)).To(Succeed())
		Expect(applicationForObject(ctx, late)).To(Equal([]reconcile.Request{req}))
		foreign := late.DeepCopy()
		foreign.Name += "-foreign"
		foreign.ResourceVersion, foreign.UID = "", ""
		foreign.Annotations[annotationApplicationUID] = "another-application"
		Expect(k8sClient.Create(ctx, foreign)).To(Succeed())
		newer := late.DeepCopy()
		newer.Name += "-newer"
		newer.ResourceVersion, newer.UID = "", ""
		newer.Annotations[annotationApplicationGeneration] = "1000"
		Expect(k8sClient.Create(ctx, newer)).To(Succeed())
		_, err = r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(
			apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(late), &corev1.Secret{})),
		).To(BeTrue())
		for _, preserved := range []*corev1.Secret{active, foreign, newer} {
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(preserved), &corev1.Secret{})).To(Succeed())
		}
		Expect(r.pruneEnvironmentSecrets(ctx, app, "")).To(Succeed())
		for _, removed := range []*corev1.Secret{active, newer} {
			Expect(
				apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(removed), &corev1.Secret{})),
			).To(BeTrue())
		}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(foreign), &corev1.Secret{})).To(Succeed())
	})

	It("fences an old reconcile after the resource returns to its original environment", func() {
		app, old, original := initialEnvironmentFixture("identity-aba")
		finishIdentityRollout(app, old)
		fresh := *old
		blocked := &pausedSecretClient{
			Client: k8sClient, namespace: getNamespace(app), name: getEnvSecretName(app),
			paused: make(chan struct{}), release: make(chan struct{}),
		}
		old.APIReader = blocked
		req := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(app)}
		done := make(chan struct{})
		var oldErr error
		var oldResult reconcile.Result
		resume := sync.OnceFunc(func() { close(blocked.release) })
		DeferCleanup(func() {
			resume()
			Eventually(done, transitionTestTimeout).Should(BeClosed())
		})
		go func() {
			defer close(done)
			oldResult, oldErr = old.Reconcile(ctx, req)
		}()
		Eventually(blocked.paused, transitionTestTimeout).Should(BeClosed())
		retargetApplication(app)
		complete := func() {
			_, err := fresh.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			retiring := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(original), retiring)).To(Succeed())
			Expect(retiring.DeletionTimestamp.IsZero()).To(BeFalse())
			retiring.Finalizers = nil
			Expect(k8sClient.Update(ctx, retiring)).To(Succeed())
			_, err = fresh.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
		}
		complete()
		stage := &corev1.Secret{}
		Expect(
			k8sClient.Get(
				ctx,
				client.ObjectKey{Namespace: app.Namespace, Name: app.Spec.ServiceSpec.Deployment.EnvSecretRef.Name},
				stage,
			),
		).To(Succeed())
		stage.Data["VALUE"] = []byte("production-new")
		stage.Labels[locov1alpha1.LabelPlacementRevision] = "5"
		Expect(k8sClient.Update(ctx, stage)).To(Succeed())
		Expect(k8sClient.Get(ctx, req.NamespacedName, app)).To(Succeed())
		app.Spec.EnvironmentID = transitionProduction
		app.Annotations[locov1alpha1.AnnotationPlacementRevision] = "5"
		app.Spec.ServiceSpec.Deployment.EnvSecretRef.Revision = 5
		Expect(k8sClient.Update(ctx, app)).To(Succeed())
		complete()
		replacement := &appsv1.Deployment{}
		depKey := client.ObjectKeyFromObject(original)
		Expect(k8sClient.Get(ctx, depKey, replacement)).To(Succeed())
		replacementUID := replacement.UID
		finishIdentityRollout(app, &fresh)
		latestStatus := app.Status.DeepCopy()
		resume()
		Eventually(done, transitionTestTimeout).Should(BeClosed())
		Expect(k8sClient.Get(ctx, req.NamespacedName, app)).To(Succeed())
		Expect(&app.Status).To(Equal(latestStatus))
		secret := &corev1.Secret{}
		Expect(
			k8sClient.Get(ctx, client.ObjectKey{Namespace: depKey.Namespace, Name: getEnvSecretName(app)}, secret),
		).To(Succeed())
		Expect(string(secret.Data["VALUE"])).To(Equal("production-new"))
		Expect(oldErr).NotTo(HaveOccurred())
		Expect(oldResult.RequeueAfter).To(Equal(deployingRequeue))
		Expect(k8sClient.Get(ctx, depKey, replacement)).To(Succeed())
		Expect(replacement.UID).To(Equal(replacementUID))
		Expect(replacement.Spec.Template.Annotations[annotationEnvSecretRevision]).To(Equal("5"))
	})

	It("fences an old reconcile that resumes after the new environment is ready to deploy", func() {
		app, old, original := initialEnvironmentFixture("identity-inflight")
		finishIdentityRollout(app, old)
		fresh := *old
		blocked := &pausedSecretClient{
			Client: k8sClient, namespace: getNamespace(app), name: getEnvSecretName(app),
			paused: make(chan struct{}), release: make(chan struct{}),
		}
		old.APIReader = blocked
		req := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(app)}
		done := make(chan struct{})
		var oldErr error
		var oldResult reconcile.Result
		resume := sync.OnceFunc(func() { close(blocked.release) })
		DeferCleanup(func() {
			resume()
			Eventually(done, transitionTestTimeout).Should(BeClosed())
		})
		go func() {
			defer close(done)
			oldResult, oldErr = old.Reconcile(ctx, req)
		}()
		Eventually(blocked.paused, transitionTestTimeout).Should(BeClosed())
		retargetApplication(app)
		_, err := fresh.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		retiring := &appsv1.Deployment{}
		depKey := client.ObjectKeyFromObject(original)
		Expect(k8sClient.Get(ctx, depKey, retiring)).To(Succeed())
		retiring.Finalizers = nil
		Expect(k8sClient.Update(ctx, retiring)).To(Succeed())
		_, err = fresh.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		replacement := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, depKey, replacement)).To(Succeed())
		replacementUID := replacement.UID
		Expect(replacementUID).NotTo(Equal(original.UID))
		finishIdentityRollout(app, &fresh)
		latestStatus := app.Status.DeepCopy()
		resume()
		Eventually(done, transitionTestTimeout).Should(BeClosed())
		Expect(k8sClient.Get(ctx, req.NamespacedName, app)).To(Succeed())
		Expect(&app.Status).To(Equal(latestStatus))
		Expect(oldErr).NotTo(HaveOccurred())
		Expect(oldResult.RequeueAfter).To(Equal(deployingRequeue))
		Expect(k8sClient.Get(ctx, depKey, replacement)).To(Succeed())
		Expect(replacement.UID).To(Equal(replacementUID))
		secret := &corev1.Secret{}
		secretKey := client.ObjectKey{Namespace: depKey.Namespace, Name: getEnvSecretName(app)}
		Expect(k8sClient.Get(ctx, secretKey, secret)).To(Succeed())
		Expect(map[string]string{
			"environment": replacement.Labels[labelEnvironmentID],
			"secret":      string(secret.Data["VALUE"]),
		}).To(Equal(map[string]string{"environment": transitionQA, "secret": "qa"}))
	})

})
