/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	locov1alpha1 "github.com/team-loco/loco/controller/api/v1alpha1"
)

var _ = Describe("Application Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default", // TODO(user):Modify as needed
		}
		application := &locov1alpha1.Application{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind Application")
			err := k8sClient.Get(ctx, typeNamespacedName, application)
			if err != nil && errors.IsNotFound(err) {
				resource := &locov1alpha1.Application{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: locov1alpha1.ApplicationSpec{
						Type:        "SERVICE",
						ResourceId:  1,
						WorkspaceId: 1,
						Region:      "us-east-1",
						ServiceSpec: &locov1alpha1.ServiceSpec{
							Deployment: &locov1alpha1.ServiceDeploymentSpec{
								Image:          "test:latest",
								Port:           8080,
								DockerfilePath: "Dockerfile",
								BuildType:      "docker",
								CPU:            "100m",
								Memory:         "128Mi",
								MinReplicas:    1,
								MaxReplicas:    3,
								Scalers: &locov1alpha1.ScalersSpec{
									Enabled:      true,
									CPUTarget:    80,
									MemoryTarget: 80,
								},
								HealthCheck: &locov1alpha1.HealthCheckSpec{
									Path:               "/health",
									Interval:           10,
									Timeout:            5,
									FailThreshold:      3,
									StartupGracePeriod: 30,
								},
								Env: map[string]string{
									"ENV": "test",
								},
							},
							Resources: &locov1alpha1.ResourcesSpec{
								CPU:    "100m",
								Memory: "128Mi",
								Replicas: locov1alpha1.ReplicasSpec{
									Min: 1,
									Max: 3,
								},
								Scalers: locov1alpha1.ScalersSpec{
									Enabled:      true,
									CPUTarget:    80,
									MemoryTarget: 80,
								},
							},
							Routing: &locov1alpha1.RoutingSpec{
								HostName:    "test-app.example.com",
								PathPrefix:  "/",
								IdleTimeout: 300,
							},
							Obs: &locov1alpha1.ObsSpec{
								Logging: locov1alpha1.LoggingSpec{
									Enabled:         true,
									RetentionPeriod: "7d",
									Structured:      true,
								},
								Metrics: locov1alpha1.MetricsSpec{
									Enabled: true,
									Path:    "/metrics",
									Port:    9090,
								},
								Tracing: locov1alpha1.TracingSpec{
									Enabled:    true,
									SampleRate: "0.1",
									Tags: map[string]string{
										"service": "test",
									},
								},
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &locov1alpha1.Application{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance Application")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		// It("should successfully reconcile the resource", func() {
		// 	By("Reconciling the created resource")
		// 	controllerReconciler := &LocoResourceReconciler{
		// 		Client: k8sClient,
		// 		Scheme: k8sClient.Scheme(),
		// 	}

		// 	_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
		// 		NamespacedName: typeNamespacedName,
		// 	})
		// 	Expect(err).NotTo(HaveOccurred())
		// 	// TODO(user): Add more specific assertions depending on your controller's reconciliation logic.
		// 	// Example: If you expect a certain status condition after reconciliation, verify it here.
		// })
	})
})
