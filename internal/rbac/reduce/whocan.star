# The reducer of who-can, per cluster: each subject that can perform params verb on params resource
# (params apiGroup, params name) in params namespace, or anywhere when it is empty, as
# rbac.WhoCanInCluster finds them. cluster.star is prepended.

BUILTIN_CLUSTER_ROLES = ["cluster-admin", "admin", "edit", "view"]

def matches_any(entries, want):
    for e in entries:
        if e == "*" or e == want:
            return True
    return False

def resource_entry_matches(entry, resource):
    if entry == "*":
        return True
    entry_segs, query_segs = entry.split("/"), resource.split("/")
    if len(entry_segs) != len(query_segs):
        return False
    for i in range(len(entry_segs)):
        if entry_segs[i] != "*" and entry_segs[i] != query_segs[i]:
            return False
    return True

def rule_matches(rule, q):
    if not rule["resources"]:
        return False
    if not matches_any(rule["verbs"], q["verb"]) or not matches_any(rule["apiGroups"], q["apiGroup"]):
        return False
    resource_matched = False
    for entry in rule["resources"]:
        if resource_entry_matches(entry, q["resource"]):
            resource_matched = True
            break
    if not resource_matched:
        return False
    return not rule["resourceNames"] or not q["name"] or q["name"] in rule["resourceNames"]

def labels_match(labels, selector):
    for k, v in selector.items():
        if labels.get(k) != v:
            return False
    return True

# effective_rules unions the rules of a role and, for an aggregating ClusterRole, of every
# ClusterRole it aggregates, transitively. Starlark has no while; each pass goes one level deeper,
# and there are at most as many levels as roles.
def effective_rules(index, roles):
    collected = {}
    pending = [index]
    for _ in range(len(roles) + 1):
        if not pending:
            break
        following = []
        for i in pending:
            if i in collected:
                continue
            collected[i] = True
            for sel in roles[i]["aggregationSelectors"]:
                for j, candidate in enumerate(roles):
                    if candidate["kind"] == "ClusterRole" and j not in collected and labels_match(candidate["labels"], sel):
                        following.append(j)
        pending = following
    rules = []
    for i in collected:
        rules.extend(roles[i]["rules"])
    return rules

def subject_key(s):
    if "namespace" in s:
        return s["kind"] + ":" + s["namespace"] + "/" + s["name"]
    return s["kind"] + ":" + s["name"]

def reduce(rows):
    q = {
        "verb": params.get("verb", ""),
        "resource": params.get("resource", ""),
        "apiGroup": params.get("apiGroup", ""),
        "name": params.get("name", ""),
    }
    namespace = params.get("namespace", "")
    cluster, roles, bindings = cluster_of(rows)
    grants = []
    for binding in bindings:
        if binding["kind"] == "RoleBinding" and namespace and binding["namespace"] != namespace:
            continue
        index = resolve_role(binding, roles)
        builtin = False
        if index >= 0:
            matched = False
            for rule in effective_rules(index, roles):
                if rule_matches(rule, q):
                    matched = True
                    break
            if not matched:
                continue
        elif binding["roleRef"]["name"] in BUILTIN_CLUSTER_ROLES or binding["roleRef"]["name"].startswith("system:"):
            # Of the built-in roles, only cluster-admin's rules are known.
            if binding["roleRef"]["name"] != "cluster-admin":
                continue
            builtin = True
        else:
            continue
        for s in binding["subjects"]:
            grants.append({"Path": subject_key(s), "Value": {
                "cluster": cluster,
                "subject": subject_key(s),
                "scope": scope_of(binding),
                "roleRef": binding["roleRef"]["name"],
                "roleRefKind": binding["roleRef"]["kind"],
                "builtin": builtin,
                "binding": binding["name"],
                "unit": binding["unit"],
            }})
    return grants
