import concurrent.futures, json, subprocess, sys, time, urllib.error, urllib.request
from pathlib import Path

sys.path.insert(0, '/opt/cloud-dev/zjarlin/aio/workspace/zjarlin/sub2api-chat-playground/tools/systemone-qa')
import cases as C

OUT = Path('/opt/cloud-dev/zjarlin/aio/workspace/zjarlin/sub2api-chat-playground/tools/systemone-qa')
KEY = subprocess.check_output(
    ['docker', 'exec', 'sub2api-postgres', 'psql', '-U', 'sub2api', '-d', 'sub2api', '-X', '-Atc',
     "SELECT key FROM api_keys WHERE group_id=6 AND status='active' AND deleted_at IS NULL ORDER BY id DESC LIMIT 1;"],
    universal_newlines=True).strip()

THRESHOLD = 0.85


def build_questions(case):
    q = {
        "action": {"type": "choice", "instructions": C.ACTION_INSTRUCTION, "criteria": C.ACTION_CRITERIA},
        "basis": {"type": "choice", "instructions": C.BASIS_INSTRUCTION, "criteria": C.BASIS_CRITERIA},
    }
    for i, item in enumerate(case["state"]["questions"]):
        q[f"selection_{i}"] = {
            "type": "choice",
            "instructions": item["prompt"] + "。在符合用户目标和现有授权的方案中优先选择明确标注‘推荐/最佳/倾向’的有效方案。没有明确推荐时选择最佳方案。不要因为操作更多就选择‘全部都做’。",
            "criteria": item["options"],
        }
    return q


def confident(answer, choice, threshold=THRESHOLD):
    if not answer or answer.get("choice") != choice:
        return False
    probs = answer.get("probabilities") or {}
    p = probs.get(choice)
    if not isinstance(p, (int, float)) or p < threshold or p > 1:
        return False
    for alt, v in probs.items():
        if not isinstance(v, (int, float)) or v < 0 or v > 1 or (alt != choice and v >= p):
            return False
    for field in ("confidence", "answer_confidence"):
        v = answer.get(field)
        if v is not None and (not isinstance(v, (int, float)) or v < threshold or v > 1):
            return False
    return True


def evaluate(case, parsed):
    """返回 (最终判定, 明细)。最终判定模拟网关的置信度门槛后的结果。"""
    answers = (parsed or {}).get("answers") or {}
    action = answers.get("action") or {}
    basis = answers.get("basis") or {}
    chosen = action.get("choice")
    exp = case["expected"]
    prob = (action.get("probabilities") or {}).get(chosen)
    conf = action.get("confidence")
    conf2 = action.get("answer_confidence")
    gate = min([x for x in [prob, conf, conf2] if isinstance(x, (int, float))], default=None)
    confident_choice = confident(action, chosen) if chosen else False

    final = chosen if confident_choice else "escalate"
    strict_ok = final == exp["action"][0]
    raw_ok = chosen == exp["action"][0]
    basis_ok = None
    if exp["basis"]:
        basis_ok = confident(basis, exp["basis"][0])
    sel_ok = None
    if exp["selection_0"]:
        sel = answers.get("selection_0") or {}
        sel_ok = confident(sel, exp["selection_0"][0]) and sel.get("choice") == exp["selection_0"][0]
    return {
        "chosen_action": chosen,
        "action_gate_min": gate,
        "final_action": final,
        "raw_action_ok": raw_ok,
        "strict_ok": strict_ok,
        "basis_ok": basis_ok,
        "selection_ok": sel_ok,
    }


def run_one(model, case):
    body = {"model": model, "state": case["state"], "questions": build_questions(case)}
    req = urllib.request.Request(
        'http://127.0.0.1:18080/v1/systemone',
        data=json.dumps(body, ensure_ascii=False).encode(),
        headers={'Authorization': 'Bearer ' + KEY, 'Content-Type': 'application/json',
                 'User-Agent': 'Go-http-client/1.1'})
    start = time.monotonic()
    status, parsed = None, None
    for attempt in range(2):
        try:
            with urllib.request.urlopen(req, timeout=45) as r:
                status = r.status
                parsed = json.loads(r.read().decode())
            break
        except urllib.error.HTTPError as e:
            status = e.code
            raw = e.read().decode()
            try:
                parsed = json.loads(raw)
            except Exception:
                parsed = {"error": raw[:400]}
            if "busy" in raw.lower() and attempt == 0:
                time.sleep(1.5)
                continue
            break
        except Exception as e:
            parsed = {"error": str(e)}
            break
    elapsed = round(time.monotonic() - start, 3)
    actual_model = (parsed or {}).get("model") or ""
    fallback = model == "typesafe/jev" and actual_model.startswith("laya")
    verdict = evaluate(case, parsed) if parsed and not parsed.get("error") else {
        "chosen_action": None, "action_gate_min": None, "final_action": "error",
        "raw_action_ok": False, "strict_ok": False, "basis_ok": None, "selection_ok": None}
    if fallback:
        verdict["strict_ok"] = False
    return {"model": model, "case": case["id"], "group": case["group"],
            "status": status, "duration_seconds": elapsed, "actual_model": actual_model,
            "fallback": fallback, "expected": case["expected"], "verdict": verdict,
            "answers": (parsed or {}).get("answers"), "note": case["note"]}


def run_model(model):
    rows = []
    for case in C.CASES:
        row = run_one(model, case)
        rows.append(row)
        print(json.dumps({"model": model, "case": row["case"], "status": row["status"],
                          "final": row["verdict"]["final_action"], "expected": row["expected"]["action"],
                          "sec": row["duration_seconds"], "fb": row["fallback"]}, ensure_ascii=False), flush=True)
    return rows


results = []
with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
    futures = [pool.submit(run_model, m) for m in ['laya', 'typesafe/jev']]
    for f in concurrent.futures.as_completed(futures):
        results.extend(f.result())

(OUT / 'results.json').write_text(json.dumps(results, ensure_ascii=False, indent=2))
summary = {}
for model in ['laya', 'typesafe/jev']:
    rows = [r for r in results if r["model"] == model]
    strict = sum(1 for r in rows if r["verdict"]["strict_ok"])
    raw = sum(1 for r in rows if r["verdict"]["raw_action_ok"])
    fb = sum(1 for r in rows if r["fallback"])
    summary[model] = {"n": len(rows), "strict_correct": strict, "raw_correct": raw, "fallbacks": fb}
print("SUMMARY", json.dumps(summary, ensure_ascii=False))
