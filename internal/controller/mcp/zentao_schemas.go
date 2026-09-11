package mcp

// Input schemas for the composite ZenTao tools. They are written by hand
// because these tools have no OpenAPI operation to generate a schema from.

const schemaResolveScope = `{
  "type": "object",
  "properties": {
    "keyword": {
      "type": "string",
      "description": "名称片段，例如产品名、项目名、迭代名或人员姓名/账号；留空表示列出可访问的条目"
    },
    "kinds": {
      "type": "array",
      "items": {"type": "string", "enum": ["product", "project", "execution", "program", "user"]},
      "description": "要查找的对象类型，默认 product/project/execution/user"
    },
    "limit": {"type": "integer", "minimum": 1, "maximum": 50, "description": "返回条数，默认 10"},
    "maxScan": {"type": "integer", "minimum": 1, "maximum": 2000, "description": "每种类型最多扫描的记录数，默认 300"}
  },
  "additionalProperties": false
}`

const schemaSearchBugs = `{
  "type": "object",
  "properties": {
    "scope": {
      "type": "string",
      "enum": ["product", "project", "execution"],
      "description": "在哪一级查找，默认 product"
    },
    "scopeID": {"type": "integer", "description": "scope 对应的 ID"},
    "scopeIDs": {"type": "array", "items": {"type": "integer"}, "description": "同时查询多个同类 scope 的 ID"},
    "productID": {"type": "integer", "description": "产品 ID，等价于 scope=product + scopeID"},
    "projectID": {"type": "integer", "description": "项目 ID，等价于 scope=project + scopeID"},
    "executionID": {"type": "integer", "description": "执行/迭代 ID，等价于 scope=execution + scopeID"},
    "keyword": {"type": "string", "description": "标题、重现步骤和关键词中的模糊匹配文本"},
    "status": {"type": "string", "description": "Bug 状态：active、resolved、closed，或 all"},
    "resolution": {"type": "string", "description": "解决方案：fixed、postponed、bydesign、duplicate、notrepro、willnotfix"},
    "type": {"type": "string", "description": "Bug 类型，例如 codeerror、config、interface、designdefect"},
    "severity": {"type": "integer", "description": "严重程度 1-4"},
    "pri": {"type": "integer", "description": "优先级 1-4"},
    "module": {"type": "integer", "description": "所属模块 ID"},
    "assignedTo": {"type": "string", "description": "当前指派人，可用账号或姓名"},
    "openedBy": {"type": "string", "description": "创建人，可用账号或姓名"},
    "resolvedBy": {"type": "string", "description": "解决人，可用账号或姓名"},
    "month": {"type": "string", "description": "创建月份 YYYY-MM，等价于设置 openedAfter/openedBefore"},
    "openedAfter": {"type": "string", "description": "创建日期下界 YYYY-MM-DD（含）"},
    "openedBefore": {"type": "string", "description": "创建日期上界 YYYY-MM-DD（含）"},
    "resolvedAfter": {"type": "string", "description": "解决日期下界 YYYY-MM-DD（含）"},
    "resolvedBefore": {"type": "string", "description": "解决日期上界 YYYY-MM-DD（含）"},
    "orderBy": {"type": "string", "description": "上游排序，例如 id_desc、severity_asc，默认 id_desc"},
    "limit": {"type": "integer", "minimum": 1, "maximum": 200, "description": "返回的 Bug 条数，默认 20"},
    "maxScan": {"type": "integer", "minimum": 1, "maximum": 5000, "description": "最多扫描的 Bug 数量，默认 600"}
  },
  "additionalProperties": false
}`

const schemaAnalyzeBug = `{
  "type": "object",
  "properties": {
    "bugID": {"type": "integer", "description": "要分析的 Bug ID"},
    "includeRelated": {"type": "boolean", "description": "是否查找同产品的相似 Bug，默认 true"},
    "relatedLimit": {"type": "integer", "minimum": 1, "maximum": 20, "description": "相似 Bug 条数，默认 5"},
    "commentLimit": {"type": "integer", "minimum": 1, "maximum": 100, "description": "最多返回的备注条数，默认全部"}
  },
  "required": ["bugID"],
  "additionalProperties": false
}`

const schemaAIScore = `{
  "type": "object",
  "properties": {
    "objectType": {
      "type": "string",
      "enum": ["task", "bug", "story", "testcase"],
      "description": "对象类型，默认 task"
    },
    "ids": {"type": "array", "items": {"type": "integer"}, "description": "要查询的对象 ID 列表"},
    "scope": {
      "type": "string",
      "enum": ["product", "project", "execution"],
      "description": "不传 ids 时，从该 scope 取最近的对象"
    },
    "scopeID": {"type": "integer", "description": "scope 对应的 ID"},
    "executionID": {"type": "integer", "description": "执行/迭代 ID，等价于 scope=execution + scopeID"},
    "productID": {"type": "integer", "description": "产品 ID，等价于 scope=product + scopeID"},
    "projectID": {"type": "integer", "description": "项目 ID，等价于 scope=project + scopeID"},
    "includeComments": {"type": "boolean", "description": "是否返回每条备注的评分和摘要，默认 true"},
    "limit": {"type": "integer", "minimum": 1, "maximum": 50, "description": "最多检查的对象数量，默认 20"}
  },
  "additionalProperties": false
}`

const schemaQualityReport = `{
  "type": "object",
  "properties": {
    "scope": {
      "type": "string",
      "enum": ["product", "project", "execution"],
      "description": "统计范围，默认 product"
    },
    "scopeID": {"type": "integer", "description": "scope 对应的 ID"},
    "productID": {"type": "integer", "description": "产品 ID，等价于 scope=product + scopeID"},
    "projectID": {"type": "integer", "description": "项目 ID，等价于 scope=project + scopeID"},
    "executionID": {"type": "integer", "description": "执行/迭代 ID，等价于 scope=execution + scopeID"},
    "month": {"type": "string", "description": "统计月份 YYYY-MM，按 Bug 创建时间过滤"},
    "openedAfter": {"type": "string", "description": "创建日期下界 YYYY-MM-DD（含）"},
    "openedBefore": {"type": "string", "description": "创建日期上界 YYYY-MM-DD（含）"},
    "assignedTo": {"type": "string", "description": "只统计指派给该账号的 Bug"},
    "topN": {"type": "integer", "minimum": 1, "maximum": 50, "description": "模块和责任人榜单长度，默认 10"},
    "maxScan": {"type": "integer", "minimum": 1, "maximum": 5000, "description": "最多扫描的 Bug 数量，默认 1000"}
  },
  "additionalProperties": false
}`

const schemaUserWorklog = `{
  "type": "object",
  "properties": {
    "account": {"type": "string", "description": "禅道账号或姓名，例如 chenpenglie"},
    "month": {"type": "string", "description": "统计月份 YYYY-MM"},
    "from": {"type": "string", "description": "开始日期 YYYY-MM-DD（含）"},
    "to": {"type": "string", "description": "结束日期 YYYY-MM-DD（含）"},
    "executionIDs": {"type": "array", "items": {"type": "integer"}, "description": "要检查的执行/迭代 ID"},
    "productIDs": {"type": "array", "items": {"type": "integer"}, "description": "要检查的产品 ID"},
    "projectIDs": {"type": "array", "items": {"type": "integer"}, "description": "要检查的项目 ID"},
    "kinds": {
      "type": "array",
      "items": {"type": "string", "enum": ["task", "bug", "story"]},
      "description": "要统计的对象类型，默认全部"
    },
    "limit": {"type": "integer", "minimum": 1, "maximum": 300, "description": "返回条数，默认 50"},
    "maxScan": {"type": "integer", "minimum": 1, "maximum": 5000, "description": "所有 scope 合计最多扫描的记录数，默认 800"}
  },
  "required": ["account"],
  "additionalProperties": false
}`

const schemaFindSimilarBugs = `{
  "type": "object",
  "properties": {
    "symptom": {
      "type": "string",
      "description": "现象描述。尽量照抄现网反馈的原话，并带上设备型号（如 DCMG150、FXM6000）、告警码或日志片段"
    },
    "productID": {"type": "integer", "description": "只在该产品内查找；不传则跨全部已索引产品"},
    "type": {"type": "string", "description": "Bug 类型，例如 codeerror、config、interface、designdefect"},
    "resolution": {"type": "string", "description": "解决方案：fixed、postponed、bydesign、duplicate、notrepro、willnotfix"},
    "fixedOnly": {"type": "boolean", "description": "只返回已修复（resolution=fixed）的历史问题，默认 false"},
    "openedAfter": {"type": "string", "description": "创建日期下界 YYYY-MM-DD（含）"},
    "openedBefore": {"type": "string", "description": "创建日期上界 YYYY-MM-DD（含）"},
    "limit": {"type": "integer", "minimum": 1, "maximum": 25, "description": "返回条数，默认 8"}
  },
  "required": ["symptom"],
  "additionalProperties": false
}`
