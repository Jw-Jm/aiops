# SL-005 r15 visual scope

Existing reference: artifacts/test-reports/ui-v12-reference-r3/SL-005-viewport.png. Fixed .field/.dialog-* CSS from ui-handoff/ops-platform-v1.2-20261020/shared/platform.css; no recapture.

Corrected r14 assumption: the reference backdrop is the login page, not overview. Reuse NativeLoginLayout as an inert, disabled, blank presentation with no authentication logic/business fetch. The original login page uses the same extracted layout and keeps its form/error/cancel handling.

Actual final geometry:660px centered, labels12/1.65,6px label/input gap, input41.4375px, help11/1.7=18.7px, footer button gap10px. Ant Design higher-specificity8px label padding and adjacent-button margin were measured and removed with narrowly scoped selectors.

Security extensions remain explicit: current-password field,12–256-codepoint policy, genuine copy and cancel logout. They alter total dialog height/position. No broad masks or whole-image acceptance asserted. r15 browser-state checks passed at schema56; subsequent footer/operator-reset checks passed at schema57.
