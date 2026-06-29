#!/usr/bin/env python3
"""Audit the bundled OpenAPI paths against a real Zentao 12.3 v1 server.

The script is intentionally conservative with destructive operations. It uses
the MCP_AUDIT_ prefix for data it creates and writes a JSON report for follow-up
schema calibration.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import time
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parents[1]
SPEC_PATH = ROOT / "docs" / "zentao-openapi.json"
REPORT_DIR = ROOT / "tmp"


class ZentaoClient:
    def __init__(self, base_url: str, account: str, password: str) -> None:
        self.base_url = base_url.rstrip("/")
        self.account = account
        self.password = password
        self.token = ""

    def login(self) -> None:
        status, body = self.request(
            "POST",
            "/tokens",
            {"account": self.account, "password": self.password},
            auth=False,
        )
        if status < 200 or status >= 300:
            raise RuntimeError(f"login failed: HTTP {status} {body[:200]}")
        data = json.loads(body or "{}")
        self.token = data["token"]

    def request(
        self,
        method: str,
        path: str,
        payload: Any | None = None,
        query: dict[str, Any] | None = None,
        auth: bool = True,
    ) -> tuple[int, str]:
        url = self.base_url + path
        if query:
            url += "?" + urllib.parse.urlencode({k: v for k, v in query.items() if v is not None})

        headers = {"Content-Type": "application/json"}
        if auth:
            headers["Token"] = self.token

        data = None
        if payload is not None:
            data = json.dumps(payload, ensure_ascii=False).encode("utf-8")

        req = urllib.request.Request(url, data=data, headers=headers, method=method)
        try:
            with urllib.request.urlopen(req, timeout=30) as resp:
                return resp.status, resp.read().decode("utf-8", "replace")
        except urllib.error.HTTPError as exc:
            return exc.code, exc.read().decode("utf-8", "replace")


def path_params(path: str) -> list[str]:
    return re.findall(r"{([^}]+)}", path)


def fill_path(path: str, fixtures: dict[str, Any]) -> tuple[str | None, list[str]]:
    missing = []
    actual = path
    for name in path_params(path):
        if name not in fixtures:
            missing.append(name)
        else:
            actual = actual.replace("{" + name + "}", str(fixtures[name]))
    return (None, missing) if missing else (actual, [])


def query_for(op: dict[str, Any]) -> dict[str, Any]:
    query: dict[str, Any] = {}
    for param in op.get("parameters", []):
        if param.get("in") != "query":
            continue
        name = param.get("name")
        if name in ("limit", "recPerPage"):
            query[name] = 1
        elif name in ("page", "pageID"):
            query[name] = 1
        elif name in ("status", "browseType"):
            query[name] = "all"
        elif name == "orderBy":
            query[name] = "id_desc"
    return query


def parse_json(body: str) -> Any:
    try:
        return json.loads(body)
    except Exception:
        return None


def sample_keys(value: Any) -> list[str]:
    if isinstance(value, dict):
        return sorted(value.keys())[:30]
    return []


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", default=os.environ.get("ZENTAO_AUDIT_BASE_URL", "http://10.70.33.19/biz/api.php/v1"))
    parser.add_argument("--account", default=os.environ.get("ZENTAO_ACCOUNT"))
    parser.add_argument("--password", default=os.environ.get("ZENTAO_PASSWORD"))
    parser.add_argument("--execution-id", default=os.environ.get("ZENTAO_AUDIT_EXECUTION_ID", "4058"))
    parser.add_argument("--product-id", default=os.environ.get("ZENTAO_AUDIT_PRODUCT_ID", "654"))
    parser.add_argument("--project-id", default=os.environ.get("ZENTAO_AUDIT_PROJECT_ID", "1292"))
    parser.add_argument("--program-id", default=os.environ.get("ZENTAO_AUDIT_PROGRAM_ID", "102"))
    parser.add_argument("--user-id", default=os.environ.get("ZENTAO_AUDIT_USER_ID", "387"))
    parser.add_argument("--task-id", default=os.environ.get("ZENTAO_AUDIT_TASK_ID", "84723"))
    parser.add_argument("--bug-id", default=os.environ.get("ZENTAO_AUDIT_BUG_ID", "60837"))
    parser.add_argument("--write", action="store_true", help="Create an audit task to validate the confirmed task-create route.")
    args = parser.parse_args()

    if not args.account or not args.password:
        raise SystemExit("ZENTAO_ACCOUNT and ZENTAO_PASSWORD are required")

    spec = json.loads(SPEC_PATH.read_text(encoding="utf-8"))
    client = ZentaoClient(args.base_url, args.account, args.password)
    client.login()

    fixtures: dict[str, Any] = {
        "userID": args.user_id,
        "programID": args.program_id,
        "productID": args.product_id,
        "projectID": args.project_id,
        "executionID": args.execution_id,
        "taskID": args.task_id,
        "bugID": args.bug_id,
        "caseID": os.environ.get("ZENTAO_AUDIT_CASE_ID", "1"),
        "testcasID": os.environ.get("ZENTAO_AUDIT_CASE_ID", "1"),
        "feedbackID": os.environ.get("ZENTAO_AUDIT_FEEDBACK_ID", "1"),
        "ticketID": os.environ.get("ZENTAO_AUDIT_TICKET_ID", "1"),
        "buildID": os.environ.get("ZENTAO_AUDIT_BUILD_ID", "1"),
        "fileID": os.environ.get("ZENTAO_AUDIT_FILE_ID", "1"),
        "planID": os.environ.get("ZENTAO_AUDIT_PLAN_ID", "1"),
        "productplanID": os.environ.get("ZENTAO_AUDIT_PLAN_ID", "1"),
        "releasID": os.environ.get("ZENTAO_AUDIT_RELEASE_ID", "1"),
        "testtaskID": os.environ.get("ZENTAO_AUDIT_TESTTASK_ID", "1"),
        "systemID": os.environ.get("ZENTAO_AUDIT_SYSTEM_ID", "1"),
        "storyID": os.environ.get("ZENTAO_AUDIT_STORY_ID", "19422"),
        "epicID": os.environ.get("ZENTAO_AUDIT_EPIC_ID", "19422"),
        "requirementID": os.environ.get("ZENTAO_AUDIT_REQUIREMENT_ID", "19422"),
    }

    prefix = "MCP_AUDIT_" + datetime.now(timezone.utc).strftime("%Y%m%d%H%M%S")
    report: dict[str, Any] = {
        "base_url": args.base_url,
        "prefix": prefix,
        "results": [],
    }

    if args.write:
        payload = {
            "name": prefix + "_task",
            "type": "test",
            "assignedTo": args.account,
            "estimate": 1,
            "left": 1,
            "estStarted": datetime.now().strftime("%Y-%m-%d"),
            "deadline": datetime.now().strftime("%Y-%m-%d"),
            "desc": "Created by Zentao MCP OpenAPI audit.",
        }
        status, body = client.request("POST", f"/executions/{args.execution_id}/tasks", payload)
        data = parse_json(body)
        report["write_probe"] = {
            "operation": "POST /executions/{executionID}/tasks",
            "status": status,
            "keys": sample_keys(data),
            "id": data.get("id") if isinstance(data, dict) else None,
        }
        if isinstance(data, dict) and data.get("id"):
            fixtures["taskID"] = data["id"]
        time.sleep(0.2)

    for path, item in sorted(spec.get("paths", {}).items()):
        for method in ("get", "post", "put", "delete", "patch"):
            op = item.get(method)
            if not op:
                continue
            actual_path, missing = fill_path(path, fixtures)
            result: dict[str, Any] = {
                "method": method.upper(),
                "path": path,
                "summary": op.get("summary", ""),
                "supported_flag": op.get("x-zentao-12_3-supported"),
            }
            if missing:
                result.update({"status": "skipped", "reason": "missing fixtures: " + ",".join(missing)})
                report["results"].append(result)
                continue

            if method != "get":
                result.update({"status": "not_executed", "reason": "mutating operation requires a dedicated fixture builder"})
                report["results"].append(result)
                continue

            status, body = client.request("GET", actual_path or path, query=query_for(op))
            data = parse_json(body)
            result.update(
                {
                    "http_status": status,
                    "status": "supported" if 200 <= status < 300 else "unsupported_or_fixture_missing",
                    "sample_keys": sample_keys(data),
                    "body_preview": body.replace("\n", " ")[:240],
                }
            )
            report["results"].append(result)

    REPORT_DIR.mkdir(exist_ok=True)
    report_path = REPORT_DIR / f"zentao_v1_audit_{prefix}.json"
    report_path.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(report_path)


if __name__ == "__main__":
    main()
