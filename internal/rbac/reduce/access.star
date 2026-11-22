# The reducer of access, per cluster: each binding naming the subject params kind, params name
# (params namespace for a ServiceAccount), with the role it grants, as rbac.SubjectAccess finds
# them. cluster.star is prepended.

def reduce(rows):
    kind, name, namespace = params.get("kind", ""), params.get("name", ""), params.get("namespace", "")
    cluster, roles, bindings = cluster_of(rows)
    held = []
    for binding in bindings:
        holds = False
        for s in binding["subjects"]:
            if s["kind"] == kind and s["name"] == name and (kind != "ServiceAccount" or s.get("namespace", "") == namespace):
                holds = True
                break
        if not holds:
            continue
        held.append({"Path": binding["name"], "Value": {
            "cluster": cluster,
            "roleRef": binding["roleRef"]["name"],
            "roleRefKind": binding["roleRef"]["kind"],
            "resolved": resolve_role(binding, roles) >= 0,
            "scope": scope_of(binding),
            "binding": binding["name"],
            "unit": binding["unit"],
        }})
    return held
