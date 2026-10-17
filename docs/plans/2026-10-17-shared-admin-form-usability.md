# Shared CMS/admin form usability — authorized scope addition

Status: active source/UI work. Authorized by Jon via parent delegation at
2026-10-17 19:37UTC, Sentinel_00962535c3d48191a20df19f113ce3a2:
HTML source input is too small; review each site-editor and multisite-admin form
for formatting and usability. This scope addition requires no new approval for
local reversible fixes. It does not authorize deployment, migration/archive,
credential/access changes or production content writes.

Own branch: `fix/shared-admin-form-usability-20261017`, isolated worktree
`cms-form-usability-20261017`, current upstream CMS main base
`2d606115f359e088e3600e791920bcc173f4b37b`. Existing source checkouts, active locked
plans and Tina's accepted saved pages remain untouched. No AGENTS.md, SKILL.md or
active plan-lock file was found in this new checkout. This new plan adds scope
without rewriting a prior frozen plan or evidence manifest.

## Inventory and measurements first

Audit every rendered form/field group owned by shared CMS Site editor and
Multisite administration, including Content/HTML, Appearance, Publishing,
preview controls, tenant creation/settings, domains/subsites and page actions.
Also inventory shared admin-shell configuration and contributed admin forms used
by multisite; attribute each finding to its upstream owner. A companion upstream
admin patch is appropriate only where the measured issue belongs there. Do not
create a Tina-specific fork or silently broaden into an unrelated redesign.

Known source entry points are `adminui/static/index.html`, `platform.html`,
`admin.css`, `admin.js` and the embedded Go handler/router contracts. The current
CSS has a global two-column form grid, `label{display:contents}`, nested editor
panes, a1280px body cap and a source textarea already declaring24rem minimum
height. Measure the actual rendered box in both embedded and fullscreen layouts
before assigning the small-editor cause; increasing a CSS number alone is not
acceptance.

Check labels/accessible names, control and textarea sizes, long values/help text,
errors and focus placement, spacing/actions, tab order, zoom and mobile overflow.
Record a bounded checklist with pass/finding/owner/fix for every audited form.

## Implementation bounds

Make the HTML source editor use the available content width and a useful
viewport-appropriate working height, with vertical resizing and a readable code
font. Preserve exact source text, native editing/selection/keyboard behavior,
unsaved drafts, pane navigation and save/reload semantics. Keep resize handles
and overflow usable on narrow screens and in the admin iframe.

Repair measured shared form layout/label/help/error/control/action issues with
reusable component styles and narrowly scoped DOM behavior. Retain site-only
versus platform routes, permission visibility, RBAC/tenant checks, CAS/history,
strict field decoding, sanitization/preview sandbox, schedules/timezone values and
source-preserving advanced HTML handling. Do not weaken security to fix layout.
No live account, page, form message or provider mutation belongs in these tests.

## Acceptance and verification

- Desktop and320/375/768px mobile/tablet layouts have no unintended horizontal
  page overflow. Text inputs, selects, labels/help/errors and actions fit their
  actual containers; wide data tables have intentional local scrolling.
- HTML source has a substantial usable viewport, fills its content column, and
  can be vertically resized. Long/multiline HTML, keyboard selection and pane
  switching preserve the complete value; save/reload preserves unrelated fields.
- Required/error states are readable, associated with the right controls, and
  keyboard reachable. Focus remains visible; ordinary/error control contrast is
  measured where applicable. Long labels/values do not overlap other controls.
- Site editor still cannot expose or call platform-only controls. Invalid saves,
  CAS conflicts and server errors retain the draft and existing security behavior.
- Add focused regressions for measured bugs using computed geometry and actual
  interactions, not tests that merely mirror CSS declarations. Reuse current
  test/browser tooling and synthetic fixtures, with no duplicated dependency cache.
- Run real isolated desktop/mobile Chromium browser QA locally against the real
  embedded HTML/CSS/JS and disclosed fixture APIs. Test embedded/fullscreen,
  resize, keyboard/error/unsaved/save/reload paths. Capture and inspect before/
  after screenshots. Never attach to the owner's active Edge context.
- Keep heavy Go/Docker builds off this Mac. Run appropriate Go/integration and
  exact-head checks on the existing authorized hosted runner path when needed.
  Open draft upstream repair PR(s) with independent review and exact-head evidence.
  Source acceptance is separate from any later package/release/deployment.

## Implemented result and audit

| Form / controls | Finding and shared repair | Verification |
| --- | --- | --- |
| Content / HTML | Hidden section navigation left the editor in the210px grid column. Span the full grid; readable14px code (16px mobile),60vh working height, native vertical resize, described help. | Actual1440px source210→1158px;768px210→646px. Native drag resize, selection, pane switching, exact advanced source and structured-body preservation pass. |
| Event tool | Global grid-column placement created implicit columns inside labels; some320px inputs were73–103px wide. Stack labeled controls at full available width. | Desktop/mobile geometry; invalid event, source/offset/timezone values, event section navigation and save pass. |
| Publishing | Explain required path and optional dates; associate timezone help with controls. | Native invalid field focus; New York browser timezone→UTC roundtrip and metadata retention pass. |
| Appearance | Explain optional template and body-only empty selection. | Native labeled input, pane navigation and preserved template pass. |
| Tenant creation | Explicit required/optional fields and associated identifier help; shared44px actions. | Native required focus, synthetic error retains fields, successful create/navigation pass. |
| Domain creation / table actions | Explain hostname and optional subsite; shared sized actions. Keep deliberate table-local scrolling. | Synthetic create/error/reset;320/375px layout and bounded errors pass. |
| Cache / preview / delete actions | Shared44px buttons and readable destructive action colors. Preview sandbox unchanged. | Geometry, keyboard focus, preview sandbox/referrer policy pass. No live action invoked. |
| Notifications | Errors vanished after3.5s; earlier timers could hide newer notices. Clear prior timer; persist errors with keyboard-accessible dismissal, bounded wrapped/scrollable text, white focus on colored background. | CAS409 draft retained; keyboard :focus-visible solid3px white outline; persistent/dismissed errors and320px error geometry pass. |
| Shared admin configuration / login | Separate upstream admin shell had260px minimum tracks, intrinsic long-label/select widths, and error text expanding entire form; missing programmatic help association. | Companion isolated `workflow-plugin-admin` branch fixes shrinking/wrapping, control borders/focus, help/status semantics. Actual config/login forms pass at1440/768/375/320px with long options/labels/help/server errors; synthetic validate/apply, secret clearing, disabled state and session bearer isolation pass. |

Both browser harnesses execute shipped UI against explicit synthetic loopback
memory APIs in fresh headless profiles; they do not exercise hosted authorization,
PostgreSQL durability, the owner's browser, or real mail. CMS iframe measurements
use a disclosed minimal wrapper, not a claim of hosted shell QA. Desktop/mobile
screenshots were inspected. No new local dependency cache or heavy Go/Docker build
was used. Hosted exact-head browser jobs retain screenshots/results for7days.
The existing Go/integration checks remain required before merge. Contributed
My Profile/Admins, authorization, onboarding and preview-agent tools have separate
upstream implementations; this patch's actual-browser coverage is the two named
CMS workspaces and generic shell configuration/login forms, not every contributed
tool or a hosted full-app acceptance run.

Local evidence outside the source checkout:
`verification/cms-forms-before-20261017`, `cms-forms-after-20261017`,
`admin-forms-before-20261017`, `admin-forms-after-20261017` contain screenshots and
computed geometry. Reproduce using the documented standalone browser commands;
`--measure-only` records a baseline without repair assertions. Do not overwrite
older task evidence or accepted review content.

## Rollout boundary and added contact scope

Source review/PR acceptance is separate from a later reviewed CMS/admin package
release, host dependency adoption, isolated review deployment and hosted browser
acceptance. No release, deployment, CMS migration/archive, credential change or
production content publication is part of this patch.

Jon subsequently supplied `tina@langevin.me` and required an authorized-admin-
editable per-site recipient. Implement contact delivery separately from these
layout repairs, with explicit review/production routing and verified transport.
The existing review form remains a no-send demo until that integration is
configured and deployed. Never use the admin identity as an inferred recipient.
