# Loco UI contracts

## Business sources

| Concern | Source | UI consequence |
| --- | --- | --- |
| Resource ownership | proto/loco/resource/v1/resource.proto; api/service/resource.go | Create in the selected environment and filter services by their owning environment, including undeployed services. |
| Stack ownership | proto/loco/infra/v1/infra.proto; api/service/infra.go | Show stack name and service key in resource details. Dashboard changes become drift in the next plan. |
| Permissions | api/tvm/verify.go; api/tvm/stack.go | Environment grants sit between workspace and resource grants. Server authorization controls every operation. |
| Plan lifecycle | api/service/infra_apply.go | Saved plans can become stale; a failed request preserves review inputs. |

## Canonical UI map

| Capability | Owner | Variants | Evidence |
| --- | --- | --- | --- |
| Forms | components/design/Field, Input, Button | Existing create/edit flows | Web lint/typecheck and browser review |
| Select/Listbox | Existing design ToggleGroup and environment menu | Authored popup | Browser keyboard and narrow viewport review |
| Dialog | components/design/Dialog | Service and credential creation | Existing sibling flows |
| Toast | components/design/Sonner; lib/error-handler.ts | Success and Connect error | Web lint and browser review |
| CRUD | pages/dashboard/DraftDrawer.tsx | Create then watch deployment | Existing flow with environment included in CreateResource |
| Scrollbar | web/src/index.css | Existing global geometry | Browser review |
| Navigation | context/ContextProvider; hooks/useEnvironment | URL environment plus persisted workspace | Dashboard environment isolation review |

## Infrastructure flow ledger

Service creation submits the active environment ID. A pending request disables duplicate submissions; successful creation proceeds to deployment watching; failures retain the draft and display the formatted error. Switching environment changes the visible resource set and keeps undeployed services with their owner.

Resource details show the owning stack and stable service key using the shared Badge. This is informational text with no hidden hover-only value.

Token access selection groups resources under their owning environment. Existing workspace and organization grants imply access through that hierarchy. Stack-restricted deployment credentials are created through the CLI; their API enforcement does not depend on UI restrictions.

## Accessibility and state

Reuse existing focus, keyboard, dialog and disabled states. Check light and dark themes and a narrow viewport. Do not introduce browser dialogs or additional effect-driven state. Repository-wide historical accessibility findings are tracked separately from the infrastructure changes; these contracts describe the owners and changed workflows.
