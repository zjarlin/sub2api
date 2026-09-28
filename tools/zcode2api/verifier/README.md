# Start Plan Browser Verification

This optional companion uses Electron 41.10.7 and the official Aliyun Captcha SDK,
following ZCode 3.14.3's Electron 41 runtime with security patches. The Go adapter remains a
standard-library-only module.
The companion receives client attribution fields, never a ZCode login JWT.
Each request obtains one fresh proof. Proofs are neither stored nor replayed.

## Run

```sh
npm ci
export ZCODE_VERIFY_KEY='<random shared secret>'
npm start
```

The default listener is `127.0.0.1:7866`, and the browser is visible. It uses the
Electron runtime installed by `npm ci`. Set `ZCODE_VERIFY_PROFILE` to select a
persistent profile directory; Electron keeps its data in the `electron`
subdirectory so it does not share a Chromium profile from a different version.
For Chromium diagnostics, set `ZCODE_VERIFY_BROWSER` if needed and run
`npm run start:chromium`; this runtime may produce different captcha decisions.
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
Electron loads a local verification document, as the native client does. The
Chromium diagnostic runtime serves an isolated page on the ZCode origin without
loading the website's redirect or application scripts. The page
and SDK stay loaded between requests, as in the native client; each request
initializes a new verification and obtains a fresh proof. Changing the captcha
configuration or cancelling an active request replaces the page.
SDK instance callbacks during challenge initialization do not start another
verification attempt for the same request.

## Interactive Verification

Both visible and headless modes try the official traceless flow first. Visible
mode opens the SDK challenge only when that flow requests interaction or does
not return in time; complete a challenge when it is displayed.
`ZCODE_VERIFY_HEADLESS=true` supports SDK traceless verification only. If the
SDK requests an interactive challenge, the service returns HTTP 409 and the
Go adapter reports `start_plan_interactive_verification_required`.
Use a visible verification browser for those requests. The service does not
solve interactive challenges, fabricate proofs, or bypass platform limits.

The Dockerfile runs Electron under Xvfb, with no physical display required.
`ZCODE_VERIFY_HEADLESS=true` allows unattended traceless verification and returns
409 if the SDK actually requests interaction. It does not change the runtime to
Chrome's headless shell. This service is not enabled for Coding Plan by default.

## Checks

```sh
npm test
npm run test:browser
```

Tests cover fresh proofs, native SDK callback outcomes, interactive challenges,
request authentication, credential exclusion, concurrency, and cancellation.
The browser regression check requires Playwright's Chromium or
`ZCODE_VERIFY_BROWSER`; it runs headlessly and checks that website redirects
cannot replace the verification page and that successive requests preserve the
SDK context while obtaining distinct proofs.
An authenticated live model request is a separate acceptance check.
