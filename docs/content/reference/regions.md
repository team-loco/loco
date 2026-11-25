---
title: "Regions"
---

A region is a place Loco runs services. Each region has a code, such as `us-ca-1`. `loco.yaml`, `--region` flags, the dashboard and the worker chart's `global.region` name a region by its code. A region is the code its clusters carry; it exists while a cluster has it.

## Codes

The API builds a region's code when an operator registers the region's first cluster with a country and a state or province:

```sh
locoadm cluster create --country us --subdivision ca --kube-context worker
```

The code is `{country}-{subdivision}-{n}`. `--country` and `--subdivision` take lowercase letters and digits, so the code is a valid DNS label; the API checks them against no list. `n` is one more than the highest number the clusters of that country and subdivision use, so the first California region is `us-ca-1` and the next `us-ca-2`. Once every cluster of a region is deleted, its code can be built again.

Each `locoadm cluster create --country --subdivision` creates a new region. Add a cluster to an existing region with `--region` and a `--name`: `locoadm cluster create --region us-ca-1 --name us-ca-1-b`. A region's first cluster takes the region code as its name when `--name` is omitted. A number separates regions in one state or province that fail independently; two clusters in one data center are one region with two clusters.

`locoadm cluster create --local` uses the development region `local-1` for kind and Docker Desktop clusters.

`locoadm region list` prints every region's code and cluster count; `loco region list` prints the regions an environment can deploy to.

## Default region

The default region is the code the API's `LOCO_DEFAULT_REGION` holds, set through the control-plane chart value `defaultRegion`. The API builds codes when clusters are registered, so set it after the first `locoadm cluster create`, which prints the code and, while no default is set, suggests the setting. `loco infra init` writes the default region into new files, `loco deploy` without a `loco.yaml` deploys to it, and the dashboard preselects it for a new service and marks it in a service's region settings. While no cluster that is not removed has the code, or the setting is unset, they use the environment's only available region instead, and `loco infra init` fails when there are several. The API logs a warning naming `LOCO_DEFAULT_REGION` the first time it finds no cluster has the code.

## Hostnames

Services get names under an app domain, such as `storefront.onloco.build` under `onloco.build`. Each worker's Gateway listens on `*.<app domain>` for every app domain in the worker chart's `global.domain.apps`, and each listener has its own Certificate for that one wildcard. Hostnames carry no region code: a service answers at the same name in every region it runs in. Point each app domain's wildcard, such as `*.onloco.build`, at the gateway of the region that serves the shared name with an A record.
