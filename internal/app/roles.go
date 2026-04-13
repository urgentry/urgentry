package app

import (
	"fmt"
	"strings"
)

type Role string

const (
	RoleAPI       Role = "api"
	RoleIngest    Role = "ingest"
	RoleWorker    Role = "worker"
	RoleScheduler Role = "scheduler"
	RoleAll       Role = "all"
)

func ParseRole(input string) (Role, error) {
	switch Role(strings.ToLower(strings.TrimSpace(input))) {
	case RoleAPI, RoleIngest, RoleWorker, RoleScheduler, RoleAll:
		return Role(strings.ToLower(strings.TrimSpace(input))), nil
	default:
		return "", fmt.Errorf("%q is not one of api|ingest|worker|scheduler|all", input)
	}
}
