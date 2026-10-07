package v1alpha1

import "testing"

func TestResourcesSpecValidate(t *testing.T) {
	valid := ResourcesSpec{CPU: "100m", Memory: "32Mi", Replicas: ReplicasSpec{Min: 1, Max: 10}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid resources rejected: %v", err)
	}
	invalid := map[string]func(*ResourcesSpec){
		"cpu below minimum":    func(r *ResourcesSpec) { r.CPU = "99m" },
		"cpu above maximum":    func(r *ResourcesSpec) { r.CPU = "2001m" },
		"memory below minimum": func(r *ResourcesSpec) { r.Memory = "31Mi" },
		"memory above maximum": func(r *ResourcesSpec) { r.Memory = "5Gi" },
		"zero min replicas":    func(r *ResourcesSpec) { r.Replicas.Min = 0 },
		"max above maximum":    func(r *ResourcesSpec) { r.Replicas.Max = 11 },
		"max below min":        func(r *ResourcesSpec) { r.Replicas = ReplicasSpec{Min: 3, Max: 2} },
	}
	for name, change := range invalid {
		t.Run(name, func(t *testing.T) {
			resources := valid
			change(&resources)
			if err := resources.Validate(); err == nil {
				t.Errorf("%+v accepted", resources)
			}
		})
	}
}

func TestDockerImagePattern(t *testing.T) {
	digest := "@sha256:7781a08afca1adb11b6294ca81ee6e04f9fc677f4f04c9d9daf2b0e068f5e89a"
	valid := []string{
		"nginx:1.31",
		"ghcr.io/team-loco/app:v1",
		"registry.loco.dev/ws-1/app" + digest,
		"loco-e2e-registry:5000/ws-1/app" + digest,
		"localhost:5001/app:latest",
	}
	for _, image := range valid {
		if !dockerImagePattern.MatchString(image) {
			t.Errorf("%q rejected", image)
		}
	}
	invalid := []string{
		"Registry/App:v1",
		"registry:port/app:v1",
		"app@sha256:short",
		"app:v1 ",
	}
	for _, image := range invalid {
		if dockerImagePattern.MatchString(image) {
			t.Errorf("%q accepted", image)
		}
	}
}
