# The map of who-can and access: each Role, ClusterRole, RoleBinding and ClusterRoleBinding as a
# record, parsed as rbac.BuildClusterRbac parses it. A field of the wrong type is dropped rather
# than failing the Unit, and a binding without a roleRef kind and name is skipped.

def strings(v):
    if type(v) != "list":
        return []
    return [x for x in v if type(x) == "string"]

def string_map(v):
    if type(v) != "dict":
        return {}
    return {k: x for k, x in v.items() if type(x) == "string"}

def rules_of(v):
    if type(v) != "list":
        return []
    return [{
        "apiGroups": strings(rule.get("apiGroups")),
        "resources": strings(rule.get("resources")),
        "verbs": strings(rule.get("verbs")),
        "resourceNames": strings(rule.get("resourceNames")),
    } for rule in v if type(rule) == "dict"]

def aggregation_selectors(v):
    if type(v) != "dict" or type(v.get("clusterRoleSelectors")) != "list":
        return []
    selectors = []
    for sel in v["clusterRoleSelectors"]:
        match_labels = string_map(sel.get("matchLabels") if type(sel) == "dict" else None)
        if match_labels:
            selectors.append(match_labels)
    return selectors

def subjects_of(v):
    if type(v) != "list":
        return []
    subjects = []
    for s in v:
        if type(s) != "dict" or type(s.get("kind")) != "string" or type(s.get("name")) != "string":
            continue
        subject = {"kind": s["kind"], "name": s["name"]}
        # A subject's key names its namespace only when it has one, as rbac.SubjectKey does.
        if type(s.get("namespace")) == "string":
            subject["namespace"] = s["namespace"]
        subjects.append(subject)
    return subjects

def extract(r):
    kind = r.get("kind")
    md = r.get("metadata")
    if type(kind) != "string" or type(md) != "dict" or type(md.get("name")) != "string":
        return []
    api_version = r.get("apiVersion")
    if type(api_version) != "string" or not api_version.startswith("rbac.authorization.k8s.io/"):
        return []
    namespace = md.get("namespace") if type(md.get("namespace")) == "string" else ""
    record = {"kind": kind, "name": md["name"]}
    if kind in ("Role", "ClusterRole"):
        record["namespace"] = namespace if kind == "Role" else ""
        record["labels"] = string_map(md.get("labels"))
        record["rules"] = rules_of(r.get("rules"))
        record["aggregationSelectors"] = aggregation_selectors(r.get("aggregationRule")) if kind == "ClusterRole" else []
    elif kind in ("RoleBinding", "ClusterRoleBinding"):
        ref = r.get("roleRef")
        if type(ref) != "dict" or type(ref.get("kind")) != "string" or type(ref.get("name")) != "string":
            return []
        record["namespace"] = namespace if kind == "RoleBinding" else ""
        record["roleRef"] = {"kind": ref["kind"], "name": ref["name"]}
        record["subjects"] = subjects_of(r.get("subjects"))
    else:
        return []
    return [{"ResourceType": api_version + "/" + kind, "ResourceName": namespace + "/" + md["name"], "Path": kind, "Value": record}]
