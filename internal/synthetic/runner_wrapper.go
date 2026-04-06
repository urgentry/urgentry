package synthetic

import "context"

type Runner struct {
	RepoRoot string
}

func (r Runner) RunScenario(ctx context.Context, name string) error {
	path, err := ResolveScenarioPath(name)
	if err != nil {
		return err
	}
	_, err = RunScenarioFile(ctx, path)
	return err
}
