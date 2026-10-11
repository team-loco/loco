package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestRunsCurrentTemplate(t *testing.T) {
	deleted := metav1.Now()
	tests := []struct {
		name              string
		annotations       map[string]string
		image             string
		deletion          *metav1.Time
		envSecretRevision string
		want              bool
	}{
		{name: "current image without secrets", image: testImage, want: true},
		{
			name:              "current image and secret revision",
			annotations:       map[string]string{annotationEnvSecretRevision: "3"},
			image:             testImage,
			envSecretRevision: "3",
			want:              true,
		},
		{
			name:              "older secret revision",
			annotations:       map[string]string{annotationEnvSecretRevision: "2"},
			image:             testImage,
			envSecretRevision: "3",
		},
		{
			name:              "secrets mounted since the replica started",
			image:             testImage,
			envSecretRevision: "3",
		},
		{name: "older image", image: "registry.example.com/app:v0"},
		{name: "terminating", image: testImage, deletion: &deleted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := &corev1.Pod{
				Annotations:       tt.annotations,
				DeletionTimestamp: tt.deletion,
				Spec:              corev1.PodSpec{Containers: []corev1.Container{{Image: tt.image}}},
			}
			got := runsCurrentTemplate(pod, testImage, tt.envSecretRevision)
			if got != tt.want {
				t.Errorf("runsCurrentTemplate = %v, want %v", got, tt.want)
			}
		})
	}
}
