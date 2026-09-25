package controller

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// podStatusHint returns the most actionable container waiting/terminated reason
// for a pod. Used so Admin can explain why auto-detect / injection cannot run
// when Ready is 0 (ImagePullBackOff, CrashLoopBackOff, etc.).
func podStatusHint(pod *corev1.Pod) (reason, message string) {
	if pod == nil {
		return "", ""
	}
	if podReady(pod) {
		return "", ""
	}

	reason, message = worstContainerHint(pod.Status.ContainerStatuses)
	if reason == "" {
		reason, message = worstContainerHint(pod.Status.InitContainerStatuses)
	}
	if reason == "" {
		switch pod.Status.Phase {
		case corev1.PodPending, corev1.PodFailed, corev1.PodUnknown:
			reason = string(pod.Status.Phase)
			message = strings.TrimSpace(pod.Status.Message)
		}
	}
	return reason, truncateStatusMessage(message, 240)
}

func worstContainerHint(statuses []corev1.ContainerStatus) (reason, message string) {
	bestScore := 0
	for _, st := range statuses {
		if st.Ready {
			continue
		}
		r, m := "", ""
		switch {
		case st.State.Waiting != nil:
			r = st.State.Waiting.Reason
			m = st.State.Waiting.Message
		case st.State.Terminated != nil && (st.State.Terminated.ExitCode != 0 || st.State.Terminated.Reason != "Completed"):
			r = st.State.Terminated.Reason
			m = st.State.Terminated.Message
			if r == "" && st.State.Terminated.ExitCode != 0 {
				r = "Error"
			}
		case st.LastTerminationState.Terminated != nil && st.RestartCount > 0:
			// Waiting often clears Reason briefly between CrashLoop restarts;
			// last termination still explains why Ready is false.
			term := st.LastTerminationState.Terminated
			r = term.Reason
			m = term.Message
			if r == "" && term.ExitCode != 0 {
				r = "CrashLoopBackOff"
			}
		}
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		score := statusReasonSeverity(r)
		if score > bestScore {
			bestScore = score
			reason, message = r, m
		}
	}
	return reason, message
}

// pickWorstStatus keeps the higher-severity reason when merging pod hints onto
// a workload (e.g. one ImagePull pod beats a Pending sibling).
func pickWorstStatus(curReason, curMsg, nextReason, nextMsg string) (string, string) {
	if nextReason == "" {
		return curReason, curMsg
	}
	if statusReasonSeverity(nextReason) > statusReasonSeverity(curReason) {
		return nextReason, nextMsg
	}
	if curReason == "" {
		return nextReason, nextMsg
	}
	if curMsg == "" && nextMsg != "" && nextReason == curReason {
		return curReason, nextMsg
	}
	return curReason, curMsg
}

func statusReasonSeverity(reason string) int {
	switch reason {
	case "InvalidImageName", "ErrImagePull", "ImagePullBackOff":
		return 100
	case "CreateContainerConfigError", "CreateContainerError":
		return 90
	case "CrashLoopBackOff", "RunContainerError", "StartError":
		return 80
	case "OOMKilled", "Error":
		return 70
	case "ContainerCannotRun", "DeadlineExceeded":
		return 60
	case "Failed", "Unknown":
		return 50
	case "Pending":
		return 30
	default:
		if reason != "" {
			return 40
		}
		return 0
	}
}

func truncateStatusMessage(msg string, max int) string {
	msg = strings.TrimSpace(msg)
	if max <= 0 || len(msg) <= max {
		return msg
	}
	return msg[:max-1] + "…"
}
