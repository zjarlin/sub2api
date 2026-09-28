# Start Plan Browser Verification

This optional companion uses Playwright and the official Aliyun Captcha SDK
used by ZCode 3.14.3. The Go adapter remains a standard-library-only module.
The companion receives client attribution fields, never a ZCode login JWT.
Each request obtains one fresh proof. Proofs are neither stored nor replayed.

## Run

```sh
npm ci
export ZCODE_VERIFY_KEY='<random shared secret>'
export ZCODE_VERIFY_BROWSER='/path/to/chromium-or-chrome'
npm start
```

The default listener is `127.0.0.1:7866`, and the browser is visible. Use a
normal Chromium/Chrome installation or the browser bundled with Playwright.
Set `ZCODE_VERIFY_PROFILE` to select a persistent browser profile directory.
Set `ZCODE_VERIFY_HOST` only when the Go adapter connects from another trusted
host. Protect that listener with a private network or TLS proxy.

Configure the Go adapter with:

```sh
Z2A_START_PLAN_VERIFIER_URL=http://127.0.0.1:7866
Z2A_START_PLAN_VERIFIER_KEY='<same shared secret>'
```

Use the same network egress for the verification browser and model requests.
The service obtains the current scene, prefix, and region from ZCode's native
`/api/v1/client/configs` endpoint. Configuration failures stop the request.
SDK verification is serialized; disconnected requests are cancelled.

## Interactive Verification

In visible mode, complete a challenge when the official SDK displays it.
`ZCODE_VERIFY_HEADLESS=true` supports SDK traceless verification only. If the
SDK requests an interactive challenge, the service returns HTTP 409 and the
Go adapter reports `start_plan_interactive_verification_required`.
Use a visible verification browser for those requests. The service does not
solve interactive challenges, fabricate proofs, or bypass platform limits.

The Dockerfile provides an optional headless service with a bundled Chromium
runtime. It is not enabled for Coding Plan deployments by default.

## Checks

```sh
npm test
```

Tests cover fresh proofs, native SDK callback outcomes, interactive challenges,
request authentication, credential exclusion, concurrency, and cancellation.
An authenticated live model request is a separate acceptance check.
