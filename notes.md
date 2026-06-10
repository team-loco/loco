For V1:

- Make loco work multi-cluster
  - this involves 2 related, but different concepts
    - being able to administer the clusters
      - ensuring updates are fully deployed.
      - correct code is synced to each cluster.
      - verifying the health, maintenance, status of each cluster.
    - choosing a cluster for deploying.
      - need a placement determining api that takes into account region, policies, current resources, environment (prod v nonprod)
  - we might need some APIs for managing the cluster, but i really don't wanna make this the responsibility of loco. the first function remains

- should certficates be created and managed in the region they are deployed?
- this should technically also be a 1-time process as well; how do we manage that.
- potential fix for this is to maybe have a designated cluster/ perhaps per region that manages stuff like this. aka the 'leader'

- Metrics/Logging/Tracing
  - created some initial setup via handrolling otel, clickhouse
  - needs attributes for mutli-tenancy, workspace specific setup.
  - All logs/tracing/metrics must include org-id/app-id/app-name/wkspc combination
  - reduce cardinality, make sure otel processor drops/submits only whats necessary.
  - dashboards must be accurate
    - for now, build our own over the obs tab, but also potentially allow grafana to be setup as an export.
  - need to create a separate admin dashboard, or use something out of the box?
  - tracing will be v2
- Logs
  - CLI table should support a simple freeze as well.
- GRPC Support
  - i believe the current implementation actually allows GRPC services, but need to double check.
- Deploy Command
  - take a token non-interactively via std in, maybe with simple output as well. `loco deploy --non-interactive --token {GH-TOKEN}`
  - take an image id, so that loco doesnt build the image and we get to skip some steps.
  - these image ids for now can only be from public container registries like ghcr.

- Builders
  - Sometimes docker client is sleeping; we need to give better errors, and maybe tell users to just specify --image if stuff keeps going wrong. we need to check the status of docker before even trying to connect to it.
  - need to ensure we can deploy OCI compliant images.
  - need to validate docker image is safe.
  - need to validate docker image is not too large!

- Service Mesh
  - need to let apps deployed in the same workspace, allowed to connect via egressing to internet
  - need some sort of tunneling/wireguard protocol

## to-do in the future

- backups for the clickhouse data as well.
- Resurrector
  - deployed separately from the cluster, and will always resurrect just one cluster.
  - need to take hourly snapshots of the cluster?
  - this can either use our postgres snapshots or etcd snapshots.

  - secrets need to pulled properly

- Loco Health Endpoint; served on status.loco.build;
- when we do multicluster, is there a cluster specific one.
- should we also get \*.loco.build
  -API latency and uptime (last 24h)
  -Builder queue backlog
  -Average deploy duration
  -“Degraded regions”
  -Current incidents (auto-created from Prometheus/Grafana alerts)

- Emailing Service?
- remove crds from helm chart
  - i think helm chart can list dependencies, but crds must be installed explicitly and separately.
  - im thinking we use fluxcd for this.
- expose the loco default url for app domain deployment to the config endpoint/defaults endpoint.

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
- actually maybe u point to actually the loco backend, and path rewrite to /revive-app?app-name=foobar123&og_url=foobar123.loco.onloco.app/cheesecake, and this revives app, and then redirects you to the correct domain again
- there is value to having an admin dashboard, for those who are planning to bring your own cloud. but need to figure out keys and roles and whatnot.

- some sort of env for configuring deployment behavior:
  - max_concurrent_app_deployments => 3

- resource management needs to be evaluated. how many resources are we using ? what are we wasting ?

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

- Tests
  - API
    - unit tests
    - integration tests
  - CLI
    - unit tests
  - UI
    - playground tests?
  - Controller
    - unit tests
    - e2e with kind

- Loco Docs.
  - we have the api docs generated via the proto definitions

---

For V2:

- Tracing
- Health Checks
  - should eventually support non-http health checks.

- Scanning Docker Images; we have a TDD for this
- Loco Packages (eventually) -> Phase II of MVP.
  - a bundle of services. always deployed to 1 wkspc.
  - maybe deploy to existing workspace.
  - support recursive deployments on the cli with the -r flag. where we discover all apps and do it?
  - should support one click deletes.

- Resurrector
- cluster management
- Profiles
- Snapshots of Cluster and backing it up.

- Custom Container Registry.

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
- introduce interactivity during login.

- saved from loco.toml:
  # deploy settings, like regions, rollback settings, predeploy postdeploy scripts?

# [Deploy]

- update deployment to first request deployment.
- this should return the container registry token short-lived, and an id the backend tied to a deployment request.
- short lived id, ttl 30 mins. this will be better for async processing for container request and whatnot
- imageTag is built on the cli; just feels weird.

- eventually use.go should be able to switch between different scopes.
- we should have a way to list all the scopes and switch between them.

- clickhouse integration to get logs, metrics, and tracing.
- need an invitations microservice alongside an emailing microservice.
- helm charts even for 'loco-core' need to be separated
  - technically, umami and loco backend api should be configurable for the UI.
- technically no longer deploying API/ UI into there.
- never return db errors directly to client, we need to clean that logic up and return a generic error message only for now.
- missing concept of schema versioning for the app config that should be scoped inside DB
- potentially setup umami for analytics on the frontend?
- whereever we make these multi saves, we need to run as a transaction.

missing a proper deployment interface as in whats happening inside allocateResources. we need a simple way to start, execute, and watch these changes.

potentially loco-api chats with loco-controller eventually.
controller-runtime would be cool.

next major todos:
lets actually finish the allocate. so the api needs to take in config of map[string][any] and we use it upstream to build the app as is.

things that are fully growing and will need a ttl:
the configmaps for apps/deployments
the data in clickhouse
the audit events.

make deploying user apps, an all or nothing approach.
is it all or nothing to deploy a single app in one region?
have a full kubernetes export function where users can literally take their loco.toml config and convert to a kubernetes yaml.
create loco resource will need to handle loco spec versions.
fully update the helm charts to be parametrized instead of using hardcoded values.
potentially use the kubernetes dashboard for admin view.

for rolling back, we will need to persist the env someplace. and unfortunately, we cannot persist in postgres.
clickhouse is named weirdly and so is our controller.

- resourcespec needs to be different per type of resoure. the current one works specifically for services.
- what is this locoresourcespec man.
- whenever we crud on any resource, we should just return the id. not the resource itself. it can be requiried to fetch the data.
- owner reference?
- cmd/deploy.go has become lost in the sauce. we need to clean it up.
- do we need tls in-cluster communication?
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
- use the out of box envoy rate limiter.

- questions:
- the deployment or create app request for loco, must be heavily rate limited.
- trace data needs to hold region/env info as well.
- try to understand whether we should do loco-api distributed in cluster itself or not.
- sure potentially clickhouse cloud, but we also need to do ttls on data. and use custom table setups.
- clusters need to be tagged with some metadata? some tolerations / some taints?
- do we need to record on our side.
  reduce scope::

- no longer let ppl bring in their custom domains.
- eventually will need canaries against our service.

- need handling of environments, on both the UI, and better handling in the backend.

- graduating ur services would be a nice to have.
- on the ui, maybe we just use toast.error() on error instead for mutations. instead of putting the value in a card.

- networking config should be nillable. if its not present, the app simply will not receive internet traffic.

- will need better api verification on builds:
  - like on docker image registry, size, ...

Super Nice to Haves:

- Respect NO_COLOR env or flag, and disable colored rendering, or other fancy utf-8 characters.
