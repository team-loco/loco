- Make loco work multi-cluster

  - likely that there will be a controlplane and a dataplane for loco.
  - controlplane can be deployed anywhere tbh. we just need a cache, service, db.
  - dataplane will need to chat with the controlplane like loco-api.
  - options:

    - karmada
    - ocm
    - custom built.

  - currently loco-api gets deployed into a cluster and manipulates kubernetes thru there
  - but we will need multi-cluster for things like different envs, as well as multi-region
  - can we get cross cluster metrics dashboards :O
  - no clue what this would look like; perhaps we actually need some cross-cluster communication.
  - aka we will need some admin apis for managing cluster as well as loco.

- should certficates be created and managed in the region they are deployed?
- this should technically also be a 1-time process as well; how do we manage that.
- potential fix for this is to maybe have a designated cluster/ perhaps per region that manages stuff like this. aka the 'leader'

- Metrics/Logging/Tracing

  - created some initial setup via handrolling otel, clickhouse, grafana.
  - we are likely pushing completely un-necessary metrics and pushing high cardinality attributes we likely do not need at all.
    - need to come up with a processor, that drops all the metrics we don't use.
    - lets use a specific allow list instead?
  - accuracy of dashboards is not clear.
  - need to create a separate admin dashboard, or use something out of the box?
  - metrics need multi-tenant support.
  - All logs/tracing/metrics must include org-id/app-id/app-name/wkspc combination
  - can potentially create dashboards dynamically, or atleast pull the data down.
  - deploy a self-hosted instance on monitoring.loco.deploy-app.com
  - tracing will be a to-do

- Profiles

  - introduce multi-profile deployments to handle dev, uat, prod deployments.
  - `loco deploy --profile=dev`
    - maybe profiles are just staging and production
  - profiles should be specifiable in loco.toml.

    ```toml
    [Profile.dev]
      CPU = "100m"
      Memory = "128Mi"
      BaseDomain = "dev.deploy-app.com"

    [Profile.prod]
      CPU = "500m"
      Memory = "1Gi"
      Replicas.Max = 5
    ```

- Health Checks

  - should eventually support non-http health checks.

- GRPC Support

  - i believe the current implementation actually allows GRPC services, but need to double check.

- Logs

  - get logs from clickhouse instead.
  - only for live tail can we grab logs from kubernetes.
  - CLI table should support a simple freeze as well.

- Deploy Command

  - take a token non-interactively via std in, maybe with simple output as well. `loco deploy --non-interactive --token {GH-TOKEN}`
  - take an image id, so that loco doesnt build the image and we get to skip some steps.
  - these image ids for now can only be from public container registries like ghcr.

- Scanning Docker Images; we have a TDD for this

- Pre-deployment loco needs to check if we can sustain the requested deployment (atleast 2x the requested resources to be safe.)

  - not sure how to do this.

- Loco CLI:

  - New Commands:
  - loco web : opens loco website in browser.
    - --dashboard, --traces? --logs, -- docs, --account
  - loco org/wks/app/account manipulation?

    - dont wanna overload the cli, i wanna keep it simple.

  - loco logout (never gonna support loco multi-account login. thats boring. logout and logback in kid.)
  - needs to build everything from
  -

- Builders

  - Sometimes docker client is sleeping; we need to give better errors, and maybe tell users to just specify --image if stuff keeps going wrong. we need to check the status of docker before even trying to connect to it.
  - need to ensure we can deploy OCI compliant images.
  - need to validate docker image is safe.
  - need to validate docker image is not too large!

- Service Mesh

  - need to let apps deployed in the same workspace, allowed to connect via egressing to internet

- Inject Loco Env Vars
  - Lets inject service URL via env variables: LOCO\_<APP_NAME>\_URL . (multiple of these, scoped to the project)
  - other env variables we can add:
    - LOCO_APP_NAME
    - LOCO_APP_VERSION ~ tied to git commit?
    - LOCO_PROFILE
    - LOCO_DEPLOYMENT_ID ~ loco's deployment id (once we have a DB and everything.)
    - LOCO_VERSION ~ loco version ? idk if we need to provide this
    - LOCO_TRACING_ENDPOINT ~ this is the openobserve endpoint to submit traces to
    - LOCO_METRICS_ENDPOINT ~ this is where loco will be scraping metrics from

## to-do in the future

- Resurrector

  - deployed separately from the cluster, and will always resurrect just one cluster.
  - maybe cluster interface with like clone cluster or something.
  - continously monitors and pings cluster health status
  - if not healthy, try to diagnose? and rebuild whats broken?
  - needs to be done on a per provider basis
  - secrets need to pulled properly
  - need to take hourly snapshots of the cluster?

- Loco Health Endpoint; served on status.deploy-app.com;
- when we do multicluster, is there a cluster specific one.
- should we also get \*.loco.deploy-app.com
  -API latency and uptime (last 24h)
  -Builder queue backlog
  -Average deploy duration
  -“Degraded regions”
  -Current incidents (auto-created from Prometheus/Grafana alerts)

- Emailing Service?

---

- Container Registry
  - Set registry lifecycle policy (start with 6 months)
  - Require image prefixing with random hash
  - Only allow registry writes from our infra, not reads
  - Store only last 2 images per project
  - Set max Docker image size (cluster limited)
  - Would be better to deploy our own manager container-registry via Harbor or similar.

---

## Low Priority

- Cleanup
  - that random config file that has too much? makes no sense
- Evaluate ArgoCD and others for better CD of kubernetes resources
- Gitlab Container Registry Token is only procured on loco deploy; should be re-procured in case node expires, ...
- Better handling of secrets related to Loco.
  - Need to be autorotated; stored in some secrets vault.
- Better handling of app secrets
- Review API contracts to make sure they make sense

- Docker image enhancements?

  - Consider a private registry that has tag-prefix/name-prefix based access-controls.
  - OSS solutions like Harbor / Quay exist.
  - come with different scanners like trivvy and multi tenant.
  - can look towards them, or for now just have a single set of deps
  - civo offers this
  - Potentially add artifact attestations to images

- Secrets
  - Kubernetes configmap of secrets needs to be created separately
  - Create RBAC to restrict secret visibility for env vars

---

Eventually...

- Support and test different deployment types: UI, cache (Redis), DB, Blob
- Respect/Allow specifying .dockerignore files / .gitignore files when building container images.
- Secrets integration

  - Secrets need to be pulled and injected
  - but user can also do this in their own container, just access aws ssm no?
  - but how are they gonna get the aws secret key and whatnot?

- how are we handling security patches?

  - depends on provider config, they will be auto managed for us if using things like fargate, otherwise our issue.
  - might need to do some sort of blue-green deployment for kubernetes.
  - what about bumping stuff like envoy gateway and things like that.
  - lets make a map of all the different projects loco is dependant on and how we can keep them updated.

- also gitlab fetch token is only valid at deployment. what if new node comes in and needs to pull down image, it cannot since gitlab token expires in like 5 mins.

may be nice to have some sort of secrets integration? like pull ur aws ssm, vault, secrets,
too much for MVP

- Next Steps:

  - Respect more of the loco.toml
    - allow setting GRPCServices and if provided, create a GRPC route, maybe we need a GRPCport?
  - loco init is chunky, introduce minimal vs full flag.

  - start design on profiles?
  - review API design; i think we are doing some funky things

---

we finally have basic logs/metrics popping up.

- organization/different streams, segregated dashboards for like workspace? project scope
- customized dashboards one for each service inside the project,
- maybe even eventually add alerts to an email.
- loco root password will need to be auto-rotated.
- switch to using grpc instead of http?
- tracing will be final step, if we even implement that piece. railway/heroku dont support tracing
- i believe missing disk metrics currently.

sleep mode; if app not used in last 7 days or something. deployment is removed; can be recreated on request.

- who sleeps the app/ who rebuilds the app?
- actually maybe u point to actually the loco backend, and path rewrite to /revive-app?app-name=foobar123&og_url=foobar123.loco.deploy-app.com/cheesecake, and this revives app, and then redirects you to the correct domain again
- there is value to having an admin dashboard, for those who are planning to bring your own cloud. but need to figure out keys and roles and whatnot.

- some sort of env for configuring deployment behavior:

  - max_concurrent_app_deployments => 3

- resource management needs to be evaluated. how many resources are we using ? what are we wasting ?

- wondering if there is value in tests where we actually literally spin up a docker container and we start running stuff on it. like literally use minikube and firing away at tests, atleast i think thats the most accurate way to test the deployment piece.
- improve ci/cd pipelines for testing purposes

- remove host from persistent flag.
- update system design diagram to represent observability.
- deploy needs to do a diff of the previous deployment done on loco, vs the incoming, and only update the resources that need changing.

  - can likely do this client side as well

- should run cleanup resources if deployment fails anywhere.

  - simple implementation is done.

- need to configure a decent HPA for the nodes themselves on kubernetes.

- does loco need to store the local path the user deployed their app from?
  - maybe we need to warn them if the provided project path has changed to ensure they arent messing things up and referencing the wrong project?
  - store mapping under $HOME/.loco?
- if we wanna continue with some gitlab container registry, we can use the container registry

- Secrets we need to manage

  - Terraform Cloud secret
  - Gitlab secret
  - Digital Ocean / Cloud provder secret for provisioning.
  - GH Oauth Client Secret (to identify)
  - Cloudflare API token so that cert-manager can issue certsa and auto-renew
  - Grafana root user secret

- deployment scripts need to actually have some tests lol
- generic webhook for notifying admins on failures.

- restrict network policies.

- otel logs, if structured, we should parse out the severity (level)

Clickhouse logs issues:

- no data stored over 30 days or X days.
- clickhouse potential sql injection with this limits + query
- queries are also relatively slow; we should index on the app-id/wkspce-id
  - this will require custom schema definition, and some manual sql work.
- introduce a way to ignore some substrings
- introduce ascending/descending timestamp order
- arbitrary filters can be added no way?
- lol is stuff being ttl'ed?
- move clickhouse monitoring to admin dashboard only
- see how to show all the fields and not just the body?
- validate clickhousedb resources we gave it. 750mb might not be enuf?

- need a full load test on loco and its services.
  - default envoy doesnt have any scaling attached?
- on successful routing, we should add the loco-tenant-id, we will be able to pull it later in otel for dashboarding? not 100% what that looks like.

- loco admin dashboard

  - see how many apps are deployed on loco
  - how many requests are currently being handled.

- theres actually tons of metrics being exported into clickhouse currently

  - we should spend some time and optimize whats being sent.
  - we should do this when we revisit the otel table structures

- for obs, we need to run cleanups after sometime for each tenant's data.
- how do we run the cleanups?

  - should this be defined as some sort of kubernetes cronjob?
  - if this is in-cluster, what if cluster crashes, any chance of data not being properly removed?

- shutdown cross cluster network traffic for namespaces with managed-by-loco.
- and then allow only if loco-workspace matches.
- namespace looks like wks-\*-app-\*

- a user's wkspace's apps must always be deployed to the same cluster.
- to reduce network chatter between their services; or else they won't be able to chat with their own network and will have to egress.
- when user deletes wkspc/app. we need to kick off metrics/logs deletion for that entire application.

  - save absolutely nothing.

- lol tests.

- on workspace / org / user creation, we need to also do the same for grafana resources.
- most likely a 1-1 mapping, with the same RBAC as well.

---

Phase I ends Here

- Loco Packages (eventually) -> Phase II of MVP.

  - a bundle of services. always deployed to 1 wkspc.
  - maybe deploy to existing workspace.
  - support recursive deployments on the cli with the -r flag. where we discover all apps and do it?
  - should support one click deletes.

- Loco UI

  - neobrutalism no cap.
  - in orange.

- Resurrector
- cluster management
- Profiles
- Snapshots of Cluster and backing it up.

- Custom Container Registry.

- Deploy Loco via a Helm Chart?

- Loco Docs.

  - we will autogenerate using code x AI. i know man.

- Health Endpoint

- Make apps sleep and then rebuild apps.

- Different app types.

- Dedicated disk for each service.

- resource consumption tests for loco; lets try to run it with as little resources as possible.

- reduce github ouath token longevity.

- split the queries into separate packages.

- setup better psql specific error handling; using something like errors.is(). i believe there is a package that can help as well.
- lets use normal ids for everything not uuid7. code will be simpler and will automatically be sortable.
- also just cheaper.

- sql unique checks should ignore the current id;
- inefficient order by in a lot of spots.

  - we order by created_at a lot. we need to add index for whereveer we do that.
  - lack of auditing. we will need an audit table? or atleast some sort of events recording.

- will use github.com/grafana/grafana-openapi-client-go to generate the grafana dashboards programatically on workspace creation?
- i think there is a better toml parser?
- introduce interactivity during login.

- saved from loco.toml:
  # deploy settings, like regions, rollback settings, predeploy postdeploy scripts?

# [Deploy]

- update deployment to first request deployment.
- this should return the container registry token short-lived, and an id the backend tied to a deployment request.
- short lived id, ttl 30 mins. this will be better for async processing for container request and whatnot
- imageTag is built on the cli; just feels weird.

- 2 tone jwt secrets.

  - basically let a jwt be parseable with 2 different secrets. (only one is really correct)
  - but this lets us swap jwt secrets?
  - could probably just do this with a kubernetes job?

- eventually use.go should be able to switch between different scopes.
- we should have a way to list all the scopes and switch between them.

- connect does not have any out of the box validation for requests coming in. we need to manually all incoming params

- create a logs service that can get logs from clickhouse or live tail the application.
- need an invitations microservice alongside an emailing microservice.
- helm chart for loco. and make loco deploy as the chart instead.
- never return db errors directly to client, we need to clean that logic up and return a generic error message only for now.
- missing concept of schema versioning for the app config that should be scoped inside DB
- potentially setup umami for analytics on the frontend?
- whereever we make these multi saves, we need to run as a transaction.
- on the UI, if API returns a message, we need to read that.

missing a proper deployment interface as in whats happening inside allocateResources. we need a simple way to start, execute, and watch these changes.

potentially loco-api chats with loco-controller eventually.
controller-runtime would be cool.

next major todos:
lets actually finish the allocate. so the api needs to take in config of map[string][any] and we use it upstream to build the app as is.

things that are fully growing and will need a ttl:
the configmaps for apps/deployments
the data in clickhouse
the audit events.

rename loco_example to loco_full_example
make deploying user apps, an all or nothing approach.
is it all or nothing to deploy a single app in one region?
have a full kubernetes export function where users can literally take their loco.toml config and convert to a kubernetes yaml.
create loco resource will need to handle loco spec versions.
fully update the helm charts to be parametrized instead of using hardcoded values.
potentially use the kubernetes dashboard for admin view.

deployment defaults should come from where?
the resource, the last deployment?
for rolling back, we will need to persist the env someplace. and unfortunately, we cannot persist in postgres.
for rolling back, how do we decide whcih deployment to push it back to? rollbacks will need to be regional
clickhouse is named weirdly and so is our controller.

- resourcespec needs to be different per type of resoure. the current one works specifically for services.
- what is this locoresourcespec man.
- whenever we crud on any resource, we should just return the id. not the resource itself. it can be requiried to fetch the data.
- owner reference?
- cmd/deploy.go has become lost in the sauce. we need to clean it up.
- do we need tls in-cluster communication?
- api needs to set and validate defaults before firing to locoresource.
- make controller an all or nothing approach.
- mark previous deployments as inactive or something before creating the next deployment. do this transactionally.
- do all the previous helm secrets and nonsense need to be removed? maybe we max history at 5.
  -add messages even when successful / deploying.
  make helm charts parametrized.
  start writing tests even.
- test scale/env. clean up cli implementation to not require config.
- should potentially be able to chop off cilium-envoy
- potentially use vtprotobuf.
- need a list regions endpoint

- ensure ppl are actually using the account, not just creating it, and leaving stuff there. so some sort of background process to clean up unused accounts, release domains, and whatnot?

- we need to create a dependency chart, on all our dependencies.
- break it down by component and whatnot.
- as long as we keep that in sync, we can always tell if we change something what will break.

- questions:
- is the current sql even correct.
- the deployment or create app request, must be heavily rate limited.
- trace data needs to hold region/env info as well.
- try to understand whether we should do loco-api distributed in cluster itself or not.
- sure potentially clickhouse cloud, but we also need to do ttls on data. and use custom table setups.
- clusters need to be tagged with some metadata? some tolerations / some taints?
- do we need to record on our side.
  reduce scope::

- no longer let ppl bring in their custom domains.
-
