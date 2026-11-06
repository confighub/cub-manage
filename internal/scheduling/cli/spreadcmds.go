// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/confighub/sdk/cliutil"

	"github.com/confighub/cub-manage/internal/kit"
	"github.com/confighub/cub-manage/internal/scheduling"
)

const (
	spreadPath       = podSpecPath + ".topologySpreadConstraints"
	affinityPath     = podSpecPath + ".affinity"
	antiAffinityPath = affinityPath + ".podAntiAffinity"
	antiPreferred    = antiAffinityPath + ".preferredDuringSchedulingIgnoredDuringExecution"
	antiRequired     = antiAffinityPath + ".requiredDuringSchedulingIgnoredDuringExecution"
	// ownPodsSelector selects the workload's own Pods, so they count toward the
	// skew (or repel each other).
	ownPodsSelector = `{"matchLabels": .spec.template.metadata.labels}`
)

// spreadConstraint is what set-topology-spread writes for one topology key.
type spreadConstraint struct {
	topologyKey        string
	maxSkew            int
	whenUnsatisfiable  string
	minDomains         int
	matchLabelKeys     []string
	nodeAffinityPolicy string
	nodeTaintsPolicy   string
}

func (c spreadConstraint) validate() error {
	if c.topologyKey == "" {
		return fmt.Errorf("--topology-key is required")
	}
	if c.maxSkew < 1 {
		return fmt.Errorf("--max-skew must be at least 1")
	}
	if c.whenUnsatisfiable != scheduling.ScheduleAnyway && c.whenUnsatisfiable != scheduling.DoNotSchedule {
		return fmt.Errorf("unknown --when-unsatisfiable %q (want %s | %s)", c.whenUnsatisfiable, scheduling.ScheduleAnyway, scheduling.DoNotSchedule)
	}
	if c.minDomains < 0 {
		return fmt.Errorf("--min-domains must be positive")
	}
	if c.minDomains > 0 && c.whenUnsatisfiable != scheduling.DoNotSchedule {
		return fmt.Errorf("--min-domains needs --when-unsatisfiable %s; the API server rejects it otherwise", scheduling.DoNotSchedule)
	}
	for flag, v := range map[string]string{"--node-affinity-policy": c.nodeAffinityPolicy, "--node-taints-policy": c.nodeTaintsPolicy} {
		if v != "" && v != "Honor" && v != "Ignore" {
			return fmt.Errorf("unknown %s %q (want Honor | Ignore)", flag, v)
		}
	}
	return nil
}

// object renders the constraint as a yq object literal, selecting the
// workload's own Pods.
func (c spreadConstraint) object() string {
	fields := []string{
		fmt.Sprintf(`"maxSkew": %d`, c.maxSkew),
		fmt.Sprintf(`"topologyKey": %q`, c.topologyKey),
		fmt.Sprintf(`"whenUnsatisfiable": %q`, c.whenUnsatisfiable),
	}
	if c.minDomains > 0 {
		fields = append(fields, fmt.Sprintf(`"minDomains": %d`, c.minDomains))
	}
	fields = append(fields, `"labelSelector": `+ownPodsSelector)
	if len(c.matchLabelKeys) > 0 {
		fields = append(fields, `"matchLabelKeys": [`+quotedList(c.matchLabelKeys)+`]`)
	}
	if c.nodeAffinityPolicy != "" {
		fields = append(fields, fmt.Sprintf(`"nodeAffinityPolicy": %q`, c.nodeAffinityPolicy))
	}
	if c.nodeTaintsPolicy != "" {
		fields = append(fields, fmt.Sprintf(`"nodeTaintsPolicy": %q`, c.nodeTaintsPolicy))
	}
	return "{" + strings.Join(fields, ", ") + "}"
}

// upsertSpreadYQ replaces the workload's spread constraint for c.topologyKey
// (whichever whenUnsatisfiable it had) with c, keeping its constraints on other
// keys. Kubernetes allows one constraint per topologyKey and whenUnsatisfiable;
// keying on topologyKey alone is what lets a re-run switch a constraint between
// soft and hard rather than add a second one.
func upsertSpreadYQ(c spreadConstraint) string {
	return fmt.Sprintf(`%s = (((%s // []) | map(select(.topologyKey != %q))) + [%s])`,
		spreadPath, spreadPath, c.topologyKey, c.object())
}

// removeSpreadYQ removes the spread constraint for topologyKey, and the list
// when nothing else is left in it.
func removeSpreadYQ(topologyKey string) string {
	return fmt.Sprintf(`%s |= ((. // []) | map(select(.topologyKey != %q))) | del(%s | select(length == 0))`,
		spreadPath, topologyKey, spreadPath)
}

// antiAffinityYQ sets (or, with remove, removes) the workload's pod
// anti-affinity term for topologyKey against its own Pods. It drops any term on
// the key from both the preferred and required lists before adding the new one,
// so switching strength replaces the term rather than adding a second, and it
// removes whatever it leaves empty.
func antiAffinityYQ(topologyKey string, required bool, weight int, remove bool) string {
	preferred := fmt.Sprintf(`((%s // []) | map(select(.podAffinityTerm.topologyKey != %q)))`, antiPreferred, topologyKey)
	requiredTerms := fmt.Sprintf(`((%s // []) | map(select(.topologyKey != %q)))`, antiRequired, topologyKey)
	term := fmt.Sprintf(`{"topologyKey": %q, "labelSelector": %s}`, topologyKey, ownPodsSelector)
	switch {
	case remove:
	case required:
		requiredTerms = "(" + requiredTerms + " + [" + term + "])"
	default:
		preferred = fmt.Sprintf(`(%s + [{"weight": %d, "podAffinityTerm": %s}])`, preferred, weight, term)
	}
	return strings.Join([]string{
		antiPreferred + " = " + preferred,
		antiRequired + " = " + requiredTerms,
		fmt.Sprintf("del(%s | select(length == 0))", antiPreferred),
		fmt.Sprintf("del(%s | select(length == 0))", antiRequired),
		fmt.Sprintf("del(%s | select(length == 0))", antiAffinityPath),
		fmt.Sprintf("del(%s | select(length == 0))", affinityPath),
	}, " | ")
}

func quotedList(vals []string) string {
	q := make([]string, 0, len(vals))
	for _, v := range vals {
		q = append(q, fmt.Sprintf("%q", v))
	}
	return strings.Join(q, ", ")
}

// newSetTopologySpreadCmd sets or removes one topology spread constraint.
func newSetTopologySpreadCmd() *cobra.Command {
	var output string
	var c spreadConstraint
	var remove bool
	var commit cliutil.CommitFlags
	cmd := &cobra.Command{
		Use:   "set-topology-spread <space>/<unit>",
		Short: "Set or remove a topology spread constraint (how evenly replicas spread across zones or nodes)",
		Long: `set-topology-spread sets the workload's topology spread constraint for one
topology key, replacing any constraint it already has on that key and keeping the
ones on other keys. The constraint selects the workload's own Pods (its
pod-template labels), so that they count toward the skew.

  set-topology-spread web-prod/web                                   # zones, soft
  set-topology-spread web-prod/web --topology-key kubernetes.io/hostname
  set-topology-spread web-prod/web --when-unsatisfiable DoNotSchedule --min-domains 3
  set-topology-spread web-prod/web --topology-key kubernetes.io/hostname --remove

  --topology-key        the node label whose values are the domains
                        (default topology.kubernetes.io/zone)
  --max-skew            how many more matching Pods one domain may have than the
                        emptiest (default 1)
  --when-unsatisfiable  ScheduleAnyway (default): prefer nodes that keep the skew;
                        DoNotSchedule: leave the Pod Pending rather than break it
  --min-domains         with DoNotSchedule, the number of domains to spread over
                        even when fewer exist yet
  --match-label-keys    Pod label keys whose values also group the Pods (default
                        pod-template-hash, so a Deployment's rollout spreads each
                        revision on its own); pass --match-label-keys= for none
  --node-taints-policy  Honor: count only nodes whose taints the Pod tolerates
                        (Kubernetes' default is Ignore, which counts tainted nodes
                        as domains the Pod can never reach)

Prefer ScheduleAnyway: DoNotSchedule leaves replicas Pending when a domain has no
room. Without a constraint of its own, a Deployment, ReplicaSet or StatefulSet
gets the cluster's default constraints ('spread' shows which).

Applies to Deployment / StatefulSet / DaemonSet / ReplicaSet / Job (pod template
at spec.template.spec). Dry-run unless --commit --change-desc; never bypasses
ValidationErrors.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var expr, what string
			if remove {
				if c.topologyKey == "" {
					return fmt.Errorf("--topology-key is required")
				}
				expr = removeSpreadYQ(c.topologyKey)
				what = fmt.Sprintf("remove topology spread on %s from %s", c.topologyKey, args[0])
			} else {
				if err := c.validate(); err != nil {
					return err
				}
				expr = upsertSpreadYQ(c)
				what = fmt.Sprintf("set topology spread on %s for %s", c.topologyKey, args[0])
			}
			changeDesc, dryRun, err := commit.Validate(what)
			if err != nil {
				return err
			}
			return runYQ(cmd, "set-topology-spread", args[0], expr, dryRun, changeDesc, output)
		},
	}
	kit.AddOutputFlag(cmd, &output)
	commit.Bind(cmd)
	cmd.Flags().StringVar(&c.topologyKey, "topology-key", scheduling.ZoneKey, "node label whose values are the spread domains")
	cmd.Flags().IntVar(&c.maxSkew, "max-skew", 1, "maximum difference in matching Pods between two domains")
	cmd.Flags().StringVar(&c.whenUnsatisfiable, "when-unsatisfiable", scheduling.ScheduleAnyway, "ScheduleAnyway | DoNotSchedule")
	cmd.Flags().IntVar(&c.minDomains, "min-domains", 0, "minimum number of domains to spread over (needs DoNotSchedule)")
	cmd.Flags().StringSliceVar(&c.matchLabelKeys, "match-label-keys", []string{"pod-template-hash"}, "Pod label keys that also group the Pods")
	cmd.Flags().StringVar(&c.nodeAffinityPolicy, "node-affinity-policy", "", "Honor | Ignore: whether nodes the Pod's node affinity excludes count as domains (Kubernetes default Honor)")
	cmd.Flags().StringVar(&c.nodeTaintsPolicy, "node-taints-policy", "", "Honor | Ignore: whether nodes with taints the Pod does not tolerate count as domains (Kubernetes default Ignore)")
	cmd.Flags().BoolVar(&remove, "remove", false, "remove the constraint on --topology-key instead of setting it")
	return cmd
}

// newSetPodAntiAffinityCmd sets or removes a pod anti-affinity term against the
// workload's own Pods.
func newSetPodAntiAffinityCmd() *cobra.Command {
	var output, topologyKey string
	var required, remove bool
	var weight int
	var commit cliutil.CommitFlags
	cmd := &cobra.Command{
		Use:   "set-pod-anti-affinity <space>/<unit>",
		Short: "Set or remove pod anti-affinity against the workload's own Pods",
		Long: `set-pod-anti-affinity keeps the workload's replicas apart: it sets a pod
anti-affinity term, on one topology key, that selects the workload's own Pods
(its pod-template labels). It replaces any term the workload already has on that
key, preferred or required, and keeps the ones on other keys.

  set-pod-anti-affinity web-prod/web                                 # preferred, per node
  set-pod-anti-affinity web-prod/web --topology-key topology.kubernetes.io/zone
  set-pod-anti-affinity web-prod/web --required
  set-pod-anti-affinity web-prod/web --remove

Preferred (the default) is weighed against other scoring; --required leaves a
replica Pending when every domain already has one, which on a per-node key blocks
scaling past the node count and a rollout's surge. For an even spread that never
blocks, 'set-topology-spread' is usually the better tool.

Applies to Deployment / StatefulSet / DaemonSet / ReplicaSet / Job (pod template
at spec.template.spec). Dry-run unless --commit --change-desc; never bypasses
ValidationErrors.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if topologyKey == "" {
				return fmt.Errorf("--topology-key is required")
			}
			if weight < 1 || weight > 100 {
				return fmt.Errorf("--weight must be between 1 and 100")
			}
			what := fmt.Sprintf("set pod anti-affinity on %s for %s", topologyKey, args[0])
			if remove {
				what = fmt.Sprintf("remove pod anti-affinity on %s from %s", topologyKey, args[0])
			}
			changeDesc, dryRun, err := commit.Validate(what)
			if err != nil {
				return err
			}
			return runYQ(cmd, "set-pod-anti-affinity", args[0], antiAffinityYQ(topologyKey, required, weight, remove), dryRun, changeDesc, output)
		},
	}
	kit.AddOutputFlag(cmd, &output)
	commit.Bind(cmd)
	cmd.Flags().StringVar(&topologyKey, "topology-key", scheduling.HostnameKey, "node label whose values are the domains replicas avoid sharing")
	cmd.Flags().BoolVar(&required, "required", false, "requiredDuringScheduling instead of preferred")
	cmd.Flags().IntVar(&weight, "weight", 100, "weight of a preferred term (1-100)")
	cmd.Flags().BoolVar(&remove, "remove", false, "remove the term on --topology-key instead of setting it")
	return cmd
}
