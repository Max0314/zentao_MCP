#!/usr/bin/env python3
"""Patch the bundled OpenAPI document for Zentao Enterprise 12.3 v1.

This script intentionally edits the JSON with a parser instead of ad-hoc text
replacement. It captures confirmed differences between the bundled document and
the real 12.3 v1 API.
"""

from __future__ import annotations

import copy
import json
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
SPEC_PATH = ROOT / "docs" / "zentao-openapi.json"


def schema_ref(name: str) -> dict:
    return {"$ref": f"#/components/schemas/{name}"}


def unsupported(op: dict, reason: str, replacement: str | None = None) -> None:
    op["deprecated"] = True
    op["x-zentao-12_3-supported"] = False
    op["x-zentao-12_3-note"] = reason
    if replacement:
        op["x-zentao-12_3-replacement"] = replacement

    prefix = "Zentao Enterprise 12.3 v1 unsupported: "
    note = prefix + reason
    if replacement:
        note += f" Use {replacement} instead."

    desc = op.get("description") or ""
    if note not in desc:
        op["description"] = (desc + "\n\n" + note).strip()

    summary = op.get("summary") or ""
    if not summary.startswith("[12.3 unsupported]"):
        op["summary"] = f"[12.3 unsupported] {summary}".strip()


def supported(op: dict, note: str | None = None) -> None:
    op["x-zentao-12_3-supported"] = True
    if note:
        op["x-zentao-12_3-note"] = note


def json_response(schema: dict, status: str = "200", description: str = "Success response") -> dict:
    return {
        status: {
            "description": description,
            "content": {
                "application/json": {
                    "schema": schema,
                }
            },
        }
    }


def path_param(name: str, description: str) -> dict:
    return {
        "name": name,
        "in": "path",
        "required": True,
        "description": description,
        "schema": {
            "oneOf": [
                {"type": "string"},
                {"type": "integer", "format": "int32"},
            ]
        },
    }


def task_payload_schema() -> dict:
    return {
        "type": "object",
        "properties": {
            "name": {"type": "string", "description": "任务名称"},
            "type": {"type": "string", "description": "任务类型，例如 devel/test/design/study/discuss/ui/affair/misc"},
            "assignedTo": {"type": "string", "description": "指派给，使用禅道账号"},
            "estStarted": {"type": "string", "format": "date", "description": "预计开始日期"},
            "deadline": {"type": "string", "format": "date", "description": "截止日期"},
            "pri": {"type": "integer", "format": "int32", "description": "优先级"},
            "estimate": {"type": "number", "description": "预计工时"},
            "left": {"type": "number", "description": "剩余工时；12.3 创建任务时建议与 estimate 一致"},
            "module": {"type": "integer", "format": "int32", "description": "所属模块"},
            "story": {"type": "integer", "format": "int32", "description": "相关需求"},
            "desc": {"type": "string", "description": "任务描述"},
        },
        "required": ["name", "type"],
        "additionalProperties": True,
    }


def patch_bug_create(paths: dict) -> None:
    op = paths.get("/bugs", {}).get("post")
    if not op:
        return

    supported(op, "Confirmed writable in Zentao Enterprise 12.3 v1.")
    op["operationId"] = "post_bugs"

    content = op.setdefault("requestBody", {}).setdefault("content", {}).setdefault("application/json", {})
    schema = content.setdefault("schema", {"type": "object"})
    props = schema.setdefault("properties", {})

    product_id = props.pop("productID", None)
    props["product"] = product_id or {
        "type": "integer",
        "format": "int32",
        "description": "所属产品",
    }
    props["product"]["description"] = "所属产品。Zentao 12.3 v1 创建 Bug 使用字段 product，而不是 productID。"

    required = schema.setdefault("required", [])
    required = ["product" if item == "productID" else item for item in required]
    if "product" not in required:
        required.insert(0, "product")
    schema["required"] = required

    example = content.setdefault("example", {})
    if "productID" in example:
        example["product"] = example.pop("productID")
    example.setdefault("product", 654)
    example.setdefault("openedBuild", ["trunk"])


def main() -> None:
    spec = json.loads(SPEC_PATH.read_text(encoding="utf-8"))

    spec["info"] = {
        **spec.get("info", {}),
        "title": "Zentao Enterprise 12.3 v1 API Docs",
        "version": "12.3-v1",
        "description": (
            "OpenAPI contract calibrated against the real Zentao Enterprise 12.3 "
            "v1 API used by this MCP service. API2.0/v2-only behavior is not assumed."
        ),
    }
    spec["servers"] = [{"url": "http://test.com/api.php/v1/"}]
    spec.setdefault("x-zentao-version", "Enterprise 12.3")
    spec.setdefault("x-zentao-api-version", "v1")

    paths = spec.setdefault("paths", {})
    patch_bug_create(paths)

    token_post = {
        "tags": ["Token"],
        "summary": "获取Token",
        "description": (
            "Zentao Enterprise 12.3 v1 token endpoint. This endpoint is used "
            "internally by the MCP server for account/password managed auth and "
            "is hidden from normal MCP tools."
        ),
        "operationId": "post_tokens",
        "x-zentao-12_3-supported": True,
        "x-mcp-internal": True,
        "security": [],
        "requestBody": {
            "required": True,
            "content": {
                "application/json": {
                    "schema": {
                        "type": "object",
                        "properties": {
                            "account": {"type": "string", "description": "用户名"},
                            "password": {"type": "string", "description": "密码"},
                        },
                        "required": ["account", "password"],
                    },
                    "example": {"account": "admin", "password": "123Qwe!@#"},
                }
            },
        },
        "responses": json_response(
            {
                "type": "object",
                "properties": {
                    "token": {"type": "string", "description": "API 凭证"},
                },
                "required": ["token"],
                "additionalProperties": True,
            },
            status="201",
            description="Token created",
        ),
    }
    paths.setdefault("/tokens", {})["post"] = token_post

    users_login = paths.get("/users/login", {}).get("post")
    if users_login:
        unsupported(
            users_login,
            "The 12.3 environment rejects /users/login for REST token login.",
            "POST /tokens",
        )
        users_login["x-mcp-internal"] = True

    tasks_post = paths.get("/tasks", {}).get("post")
    if tasks_post:
        unsupported(
            tasks_post,
            "tasksEntry::post requires an execution id in the route in Zentao 12.3 v1.",
            "POST /executions/{executionID}/tasks",
        )

    execution_tasks = paths.setdefault("/executions/{executionID}/tasks", {})
    existing_get = execution_tasks.get("get")
    create_task = copy.deepcopy(tasks_post) if tasks_post else {}
    create_task.update(
        {
            "tags": ["Task"],
            "summary": "创建任务",
            "description": (
                "Create a task under an execution. Confirmed route for Zentao "
                "Enterprise 12.3 v1."
            ),
            "operationId": "post_executions_executionID_tasks",
            "deprecated": False,
            "x-zentao-12_3-supported": True,
            "parameters": [path_param("executionID", "所属执行/迭代 ID")],
            "requestBody": {
                "required": True,
                "content": {
                    "application/json": {
                        "schema": task_payload_schema(),
                        "example": {
                            "name": "MCP_AUDIT_task",
                            "type": "test",
                            "assignedTo": "chenpenglie",
                            "estimate": 5,
                            "left": 5,
                            "estStarted": "2026-06-01",
                            "deadline": "2026-06-01",
                            "desc": "Created by MCP audit",
                        },
                    }
                },
            },
            "responses": json_response(
                {
                    "type": "object",
                    "properties": {
                        "id": {
                            "oneOf": [
                                {"type": "string"},
                                {"type": "integer", "format": "int32"},
                            ],
                            "description": "新建任务 ID",
                        }
                    },
                    "required": ["id"],
                    "additionalProperties": True,
                },
                status="201",
                description="Task created",
            ),
        }
    )
    create_task.pop("x-zentao-12_3-note", None)
    create_task.pop("x-zentao-12_3-replacement", None)
    execution_tasks["post"] = create_task
    if existing_get:
        supported(existing_get, "Confirmed readable in Zentao Enterprise 12.3 v1.")

    confirmed_gets = {
        "/products",
        "/products/{productID}",
        "/products/{productID}/bugs",
        "/executions",
        "/executions/{executionID}",
        "/executions/{executionID}/tasks",
        "/tasks/{taskID}",
        "/bugs/{bugID}",
        "/users",
        "/users/{userID}",
        "/programs",
        "/projects",
        "/projects/{projectID}/executions",
    }
    for path in confirmed_gets:
        op = paths.get(path, {}).get("get")
        if op:
            supported(op, "Confirmed readable in Zentao Enterprise 12.3 v1.")

    confirmed_writes = {
        ("/bugs/{bugID}", "put"): "Confirmed writable in Zentao Enterprise 12.3 v1.",
        ("/bugs/{bugID}", "delete"): "Confirmed writable in Zentao Enterprise 12.3 v1.",
        ("/bugs/{bugID}/resolve", "put"): "Confirmed writable in Zentao Enterprise 12.3 v1.",
        ("/bugs/{bugID}/close", "put"): "Confirmed writable in Zentao Enterprise 12.3 v1.",
        ("/tasks/{taskID}", "put"): "Confirmed writable in Zentao Enterprise 12.3 v1.",
        ("/tasks/{taskID}", "delete"): "Confirmed writable in Zentao Enterprise 12.3 v1.",
        ("/tasks/{taskID}/start", "put"): "Confirmed writable in Zentao Enterprise 12.3 v1.",
        ("/tasks/{taskID}/finish", "put"): "Confirmed writable in Zentao Enterprise 12.3 v1.",
        ("/tasks/{taskID}/close", "put"): "Confirmed writable in Zentao Enterprise 12.3 v1.",
    }
    for (path, method), note in confirmed_writes.items():
        op = paths.get(path, {}).get(method)
        if op:
            supported(op, note)

    known_unsupported = {
        "/bugs/{bugID}/activate": "Route returns 404 in the 12.3 v1 environment.",
        "/products/{productID}/epics": "Route returns 404 in the 12.3 v1 environment.",
        "/products/{productID}/feedbacks": "Route returns 404 in the 12.3 v1 environment.",
        "/products/{productID}/productplans": "Route returns 404 in the 12.3 v1 environment.",
        "/products/{productID}/requirements": "Route returns 404 in the 12.3 v1 environment.",
        "/products/{productID}/systems": "Route returns 404 in the 12.3 v1 environment.",
        "/products/{productID}/tickets": "Route returns 404 in the 12.3 v1 environment.",
        "/products/{productID}/testtasks": "Route returns 404 in the 12.3 v1 environment.",
        "/tasks/{taskID}/activate": "Route returns 404 in the 12.3 v1 environment.",
        "/executions/{executionID}/testtasks": "Route returns 404 in the 12.3 v1 environment.",
    }
    for path, reason in known_unsupported.items():
        for method in ("get", "post", "put", "delete", "patch"):
            op = paths.get(path, {}).get(method)
            if op:
                unsupported(op, reason)

    SPEC_PATH.write_text(
        json.dumps(spec, ensure_ascii=False, indent=2) + "\n",
        encoding="utf-8",
    )


if __name__ == "__main__":
    main()
