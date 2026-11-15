package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Service files", func() {
	It("mounts each file read-only at its path and removes them with the spec", func() {
		app := workspaceApp("files", "files")
		app.Spec.WorkspaceID = "ws-files"
		const path = "/otel-config.yml"
		app.Spec.ServiceSpec.Deployment.Files = map[string]string{path: "file_format: \"1.0\"\n"}
		namespace := &corev1.Namespace{Name: testWorkspaceNamespace(app)}
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, namespace)).To(Succeed()) })
		r := &ApplicationReconciler{
			Client: k8sClient, WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix, Telemetry: testTelemetry(),
		}
		configMapKey := client.ObjectKey{Namespace: namespace.Name, Name: getFilesConfigMapName(app)}
		depKey := client.ObjectKey{Namespace: namespace.Name, Name: testAppName(app)}
		reconcileFiles := func() {
			Expect(r.ensureFilesConfigMap(ctx, app)).To(Succeed())
			_, err := r.ensureDeployment(ctx, app, envSecretMount{})
			Expect(err).NotTo(HaveOccurred())
		}

		reconcileFiles()
		configMap := &corev1.ConfigMap{}
		Expect(k8sClient.Get(ctx, configMapKey, configMap)).To(Succeed())
		Expect(configMap.Data).To(Equal(map[string]string{fileKey(path): "file_format: \"1.0\"\n"}))
		dep := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, depKey, dep)).To(Succeed())
		Expect(dep.Spec.Template.Spec.Containers[0].VolumeMounts).To(Equal([]corev1.VolumeMount{{
			Name: filesVolume, MountPath: path, SubPath: fileKey(path), ReadOnly: true,
		}}))
		volumes := dep.Spec.Template.Spec.Volumes
		Expect(volumes).To(HaveLen(1))
		Expect(volumes[0].ConfigMap.Name).To(Equal(configMapKey.Name))

		app.Spec.ServiceSpec.Deployment.Files[path] = "file_format: \"1.1\"\n"
		reconcileFiles()
		Expect(k8sClient.Get(ctx, configMapKey, configMap)).To(Succeed())
		Expect(configMap.Data).To(HaveKeyWithValue(fileKey(path), "file_format: \"1.1\"\n"))

		app.Spec.ServiceSpec.Deployment.Files = nil
		reconcileFiles()
		err := k8sClient.Get(ctx, configMapKey, configMap)
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "the files ConfigMap outlived the files")
		Expect(k8sClient.Get(ctx, depKey, dep)).To(Succeed())
		Expect(dep.Spec.Template.Spec.Volumes).To(BeEmpty())
		Expect(dep.Spec.Template.Spec.Containers[0].VolumeMounts).To(BeEmpty())
	})
})
