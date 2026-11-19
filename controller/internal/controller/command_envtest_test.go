package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Container command", func() {
	It("sets and clears the container command and args", func() {
		app := workspaceApp("command", "command")
		app.Spec.EnvironmentID = "env-command"
		namespace := &corev1.Namespace{Name: testEnvironmentNamespace(app)}
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, namespace)).To(Succeed()) })
		r := &ApplicationReconciler{
			Client: k8sClient, WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix, Telemetry: testTelemetry(),
		}
		key := client.ObjectKey{Namespace: namespace.Name, Name: testAppName(app)}
		container := func() corev1.Container {
			deployment := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, key, deployment)).To(Succeed())
			return deployment.Spec.Template.Spec.Containers[0]
		}

		app.Spec.ServiceSpec.Deployment.Command = []string{"/http-echo"}
		app.Spec.ServiceSpec.Deployment.Args = []string{"-listen=:8000", "-text=hello world"}
		_, err := r.ensureDeployment(ctx, app, envSecretMount{})
		Expect(err).NotTo(HaveOccurred())
		Expect(container().Command).To(Equal([]string{"/http-echo"}))
		Expect(container().Args).To(Equal([]string{"-listen=:8000", "-text=hello world"}))

		app.Spec.ServiceSpec.Deployment.Command = nil
		app.Spec.ServiceSpec.Deployment.Args = nil
		_, err = r.ensureDeployment(ctx, app, envSecretMount{})
		Expect(err).NotTo(HaveOccurred())
		Expect(container().Command).To(BeEmpty())
		Expect(container().Args).To(BeEmpty())
	})

	It("rejects an empty command entry at the Application API boundary", func() {
		app := isolationTestApplication("ws-command-reject", "command-reject")
		app.Spec.ServiceSpec.Deployment.Command = []string{"/bin/sh", ""}
		err := k8sClient.Create(ctx, app)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("command"))
	})
})
