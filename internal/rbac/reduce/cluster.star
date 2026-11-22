# What the who-can and access reducers share: one cluster's roles and bindings, from the records the
# map returned, and how a binding resolves its role. It mirrors rbac/semantics.go.

def cluster_of(rows):
    roles, bindings = [], []
    for row in rows:
        record = dict(row["Value"])
        record["unit"] = row["Origin"]["UnitSlug"]
        if record["kind"] in ("Role", "ClusterRole"):
            roles.append(record)
        else:
            bindings.append(record)
    return rows[0]["Origin"]["TargetSlug"], roles, bindings

# resolve_role returns the index in roles of the role a binding names, or -1 when the cluster does
# not have it, as rbac.ResolveRoleRef does.
def resolve_role(binding, roles):
    ref = binding["roleRef"]
    for i, r in enumerate(roles):
        if ref["kind"] == "ClusterRole" and r["kind"] == "ClusterRole" and r["name"] == ref["name"]:
            return i
        if ref["kind"] == "Role" and binding["kind"] == "RoleBinding" and r["kind"] == "Role" and r["name"] == ref["name"] and r["namespace"] == binding["namespace"]:
            return i
    return -1

def scope_of(binding):
    return binding["namespace"] if binding["kind"] == "RoleBinding" else ""
