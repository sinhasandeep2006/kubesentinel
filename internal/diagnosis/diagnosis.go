// Package diagnosis turns evidence into ranked, explainable hypotheses using
// deterministic rules. Scores rank hypotheses; they are not probabilities.
package diagnosis

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/sinhasandeep2006/kubesentinel/internal/incident"
)

const Method = "deterministic rules v1; score = number of supporting evidence items, a ranking aid, not a probability"

type Diagnoser struct {
	KubeContext string // used only to print copy-pasteable read-only commands
}

// facts are the parts of the evidence the rules look at.
type facts struct {
	termination *incident.Evidence
	reason      string
	exitCode    int
	hasExit     bool

	liveness  *incident.Evidence // events evidence, if it shows liveness failures
	livenessN int
	logs      *incident.Evidence
	errorLine string // first log line that looks like an error
	missing   []string

	// Condition detectors (image pull, scheduling, readiness).
	cond        *incident.Evidence
	condReason  string
	condMessage string
	events      *incident.Evidence
	failed      []string // messages of Failed / FailedScheduling events
	readiness   int      // readiness probe failure events
	startup     int      // startup probe failure events

	// Prometheus usage over the window; nil means no data.
	metrics  *incident.Evidence
	memRatio *float64 // peak working set / limit
	throttle *float64 // fraction of CFS periods throttled
}

// throttleThreshold: above this, CPU throttling is worth mentioning as a factor.
const throttleThreshold = 0.25

var errorLine = regexp.MustCompile(`(?i)\b(error|fatal|panic|exception|failed|refused)\b`)

func extract(ev []incident.Evidence) facts {
	var f facts
	// Re-diagnosis sees every collection round. A source counts as missing only
	// if no round obtained it, and each gap is listed once.
	have := map[string]bool{}
	for _, e := range ev {
		if e.Availability == incident.Available {
			have[e.Type] = true
		}
	}
	seen := map[string]bool{}
	for i := range ev {
		e := &ev[i]
		if e.Availability != incident.Available {
			m := fmt.Sprintf("%s (%s: %s)", e.Summary, e.Availability, e.Detail)
			if !have[e.Type] && !seen[m] {
				seen[m] = true
				f.missing = append(f.missing, m)
			}
			continue
		}
		switch e.Type {
		case incident.EvidenceDetectorSignal:
			f.cond = e
			f.condReason, _ = e.Payload["reason"].(string)
			f.condMessage, _ = e.Payload["message"].(string)
		case incident.EvidenceTermination:
			f.termination = e
			f.reason, _ = e.Payload["reason"].(string)
			f.exitCode, f.hasExit = toInt(e.Payload["exit_code"])
		case incident.EvidenceEvents:
			f.events = e
			for _, m := range toMaps(e.Payload["events"]) {
				r, _ := m["reason"].(string)
				msg, _ := m["message"].(string)
				n, ok := toInt(m["count"]) // the API folds repeats into one event with a count
				if !ok || n < 1 {
					n = 1
				}
				switch {
				case r == "Unhealthy" && strings.Contains(msg, "Liveness probe failed"):
					f.liveness = e
					f.livenessN += n
				case r == "Unhealthy" && strings.Contains(msg, "Readiness probe failed"):
					f.readiness += n
				case r == "Unhealthy" && strings.Contains(msg, "Startup probe failed"):
					f.startup += n
				case r == "Failed" || r == "FailedScheduling":
					f.failed = append(f.failed, msg)
				}
			}
		case incident.EvidenceMetrics:
			f.metrics = e
			f.memRatio = toFloat(e.Payload["memory_ratio"])
			f.throttle = toFloat(e.Payload["cpu_throttled_ratio"])
		case incident.EvidencePreviousLogs:
			f.logs = e
			for _, l := range toStrings(e.Payload["lines"]) {
				if errorLine.MatchString(l) {
					f.errorLine = l
					break
				}
			}
		}
	}
	return f
}

func (d Diagnoser) Diagnose(inc incident.Incident, ev []incident.Evidence) []incident.Hypothesis {
	f := extract(ev)
	pod := fmt.Sprintf("-n %s %s", inc.Namespace, inc.Pod)
	kc := "kubectl"
	if d.KubeContext != "" {
		kc += " --context " + d.KubeContext
	}
	logsCmd := fmt.Sprintf("%s logs %s -c %s --previous", kc, pod, inc.Container)
	describeCmd := fmt.Sprintf("%s describe pod %s", kc, pod)
	workloadCmd := fmt.Sprintf("%s get %s %s -n %s -o yaml", kc, strings.ToLower(inc.WorkloadKind), inc.WorkloadName, inc.Namespace)

	var hs []incident.Hypothesis
	add := func(h incident.Hypothesis) {
		h.Score = len(h.Supporting)
		h.Missing = append(h.Missing, f.missing...)
		h.Method = Method
		hs = append(hs, h)
	}

	conditionRules(f, cmds{kc: kc, describe: describeCmd, workload: workloadCmd, inc: inc}, add)

	if f.reason == "OOMKilled" {
		h := incident.Hypothesis{
			Category: "memory_exhaustion",
			Title:    "Container exceeded its memory limit",
			Rationale: "The kubelet reports termination reason OOMKilled. This confirms the container was killed for memory, " +
				"not why memory ran out.",
			Supporting:   []incident.EvidenceRef{ref(f.termination, "termination reason OOMKilled")},
			Alternatives: []string{"memory leak in the application", "memory limit too low for normal load", "load spike"},
			NextSteps:    []string{describeCmd + "   # check memory requests/limits", logsCmd},
		}
		if f.liveness != nil {
			h.Contradicting = append(h.Contradicting, ref(f.liveness, "liveness probe failures also present"))
		}
		switch {
		case f.memRatio != nil && *f.memRatio >= 0.8:
			h.Supporting = append(h.Supporting, ref(f.metrics, fmt.Sprintf("peak memory reached %.0f%% of the limit", *f.memRatio*100)))
		case f.memRatio != nil:
			// Not contradicting: a spike that kills within milliseconds falls between 15s scrapes.
			h.Missing = append(h.Missing, fmt.Sprintf("the memory spike itself: the observed peak was only %.0f%% of the limit, so the spike was shorter than the scrape interval", *f.memRatio*100))
		}
		add(h)
	}

	if f.liveness != nil {
		h := incident.Hypothesis{
			Category:     "liveness_probe_failure",
			Title:        "Liveness probe failures cause the kubelet to restart the container",
			Rationale:    fmt.Sprintf("%d liveness probe failure events were recorded for the pod.", f.livenessN),
			Supporting:   []incident.EvidenceRef{ref(f.liveness, "Unhealthy: Liveness probe failed")},
			Alternatives: []string{"probe timeout or initial delay too short", "application slow or blocked", "dependency latency"},
			NextSteps:    []string{describeCmd + "   # check probe settings and events"},
		}
		if f.hasExit && (f.exitCode == 137 || f.exitCode == 143) {
			h.Supporting = append(h.Supporting, ref(f.termination, fmt.Sprintf("exit code %d is consistent with a kubelet kill", f.exitCode)))
		} else if f.hasExit && f.exitCode != 0 {
			h.Contradicting = append(h.Contradicting, ref(f.termination,
				fmt.Sprintf("exit code %d suggests the process exited by itself rather than being killed", f.exitCode)))
		}
		if r := throttledRef(f); r != nil {
			h.Supporting = append(h.Supporting, *r)
		}
		add(h)
	}

	switch {
	case !f.hasExit || f.reason == "OOMKilled":
	case f.exitCode == 0:
		add(incident.Hypothesis{
			Category: "normal_exit",
			Title:    "The process finishes and exits successfully, then is restarted",
			Rationale: "Exit code 0 means the process ended normally. With restartPolicy Always, Kubernetes restarts it, " +
				"which suggests a short-lived command running as a long-lived workload.",
			Supporting:   []incident.EvidenceRef{ref(f.termination, "exit code 0")},
			Alternatives: []string{"the command is a one-shot task that belongs in a Job", "entrypoint returns early"},
			NextSteps:    []string{workloadCmd + "   # check command and args"},
		})
	case f.exitCode == 137 || f.exitCode == 143:
		if f.liveness == nil {
			sig := map[int]string{137: "SIGKILL", 143: "SIGTERM"}[f.exitCode]
			add(incident.Hypothesis{
				Category:     "external_termination",
				Title:        fmt.Sprintf("Container was stopped by a signal (%s)", sig),
				Rationale:    fmt.Sprintf("Exit code %d means the process received %s; no OOMKilled reason or probe failures were recorded.", f.exitCode, sig),
				Supporting:   []incident.EvidenceRef{ref(f.termination, fmt.Sprintf("exit code %d", f.exitCode))},
				Alternatives: []string{"node pressure eviction", "liveness probe failure whose events have expired", "manual kill or rollout"},
				NextSteps:    []string{describeCmd},
			})
		}
	default:
		h := incident.Hypothesis{
			Category:     "application_error",
			Title:        "The application exits with an error",
			Rationale:    fmt.Sprintf("The container terminated with reason %q and non-zero exit code %d.", f.reason, f.exitCode),
			Supporting:   []incident.EvidenceRef{ref(f.termination, fmt.Sprintf("exit code %d", f.exitCode))},
			Alternatives: []string{"missing configuration or secret", "dependency unavailable at startup", "bad image or command"},
			NextSteps:    []string{logsCmd, workloadCmd + "   # check recent changes to image, command, env"},
		}
		if f.errorLine != "" {
			h.Supporting = append(h.Supporting, ref(f.logs, "log line: "+f.errorLine))
		} else if f.logs != nil {
			h.Contradicting = append(h.Contradicting, ref(f.logs, "no error-like lines in the last log lines"))
		}
		add(h)
	}

	if len(hs) == 0 {
		add(incident.Hypothesis{
			Category:  "inconclusive",
			Title:     "Not enough evidence to explain the restarts",
			Rationale: "No termination reason, exit code, or probe failures were available to the rules.",
			NextSteps: []string{describeCmd, logsCmd},
		})
	}

	sort.SliceStable(hs, func(i, j int) bool {
		return hs[i].Score-len(hs[i].Contradicting) > hs[j].Score-len(hs[j].Contradicting)
	})
	for i := range hs {
		hs[i].Rank = i + 1
	}
	return hs
}

// throttledRef supports probe-failure hypotheses: a throttled app may answer probes too slowly.
func throttledRef(f facts) *incident.EvidenceRef {
	if f.throttle == nil || *f.throttle < throttleThreshold {
		return nil
	}
	r := ref(f.metrics, fmt.Sprintf("CPU throttled in %.0f%% of periods: the app may answer probes too slowly", *f.throttle*100))
	return &r
}

func toFloat(v any) *float64 {
	var x float64
	switch n := v.(type) {
	case float64:
		x = n
	case int:
		x = float64(n)
	case int32:
		x = float64(n)
	case int64:
		x = float64(n)
	default:
		return nil
	}
	return &x
}

func ref(e *incident.Evidence, note string) incident.EvidenceRef {
	if e == nil {
		return incident.EvidenceRef{Note: note}
	}
	id := e.ID
	return incident.EvidenceRef{EvidenceID: &id, Note: note}
}

// Payload values come either straight from the collector or decoded from JSON,
// so accept both shapes.
func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	}
	return 0, false
}

func toMaps(v any) []map[string]any {
	switch l := v.(type) {
	case []map[string]any:
		return l
	case []any:
		out := make([]map[string]any, 0, len(l))
		for _, x := range l {
			if m, ok := x.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

func toStrings(v any) []string {
	switch l := v.(type) {
	case []string:
		return l
	case []any:
		out := make([]string, 0, len(l))
		for _, x := range l {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
