package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Environment identity", func() {
	It("rejects retargeting an existing Application while accepting same-environment updates", func() {
		app := isolationTestApplication("identity-workspace", "identity-resource")
		app.Spec.EnvironmentID = testProductionEnvironmentID
		Expect(k8sClient.Create(ctx, app)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, app)).To(Succeed()) })
		key := client.ObjectKeyFromObject(app)
		app.Spec.EnvironmentID = testPreviewEnvironmentID
		Expect(apierrors.IsInvalid(k8sClient.Update(ctx, app))).To(BeTrue())
		Expect(k8sClient.Get(ctx, key, app)).To(Succeed())
		Expect(app.Spec.EnvironmentID).To(Equal(testProductionEnvironmentID))
		originalUID := app.UID
		app.Spec.EnvironmentName = "renamed"
		Expect(k8sClient.Update(ctx, app)).To(Succeed())
		Expect(k8sClient.Get(ctx, key, app)).To(Succeed())
		Expect(app.UID).To(Equal(originalUID))
		Expect(app.Spec.EnvironmentName).To(Equal("renamed"))
	})
})
