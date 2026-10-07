package main

import loco "github.com/team-loco/loco/sdk/go"

func main() {
	loco.Run(func(ctx loco.Context) loco.Stack {
		replicas := int32(1)
		if ctx.EnvironmentType == "production" {
			replicas = 2
		}
		return loco.Stack{Name: "frontend", Services: []loco.Service{
			{
				Key:           "frontend",
				Build:         &loco.DockerBuild{Context: ".", Dockerfile: "Dockerfile"},
				Routing:       loco.Routing{Port: 8080},
				PrimaryRegion: "us-east-1",
				Health:        loco.Health{Path: "/"},
				Domain:        &loco.PlatformDomain{Hostname: "frontend-" + ctx.Environment + ".onloco.app"},
				Regions: map[string]loco.Region{
					"us-east-1": {CPU: "100m", Memory: "256Mi", ReplicasMin: replicas, ReplicasMax: replicas},
				},
			},
		}}
	})
}
