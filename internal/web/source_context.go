package web

import (
	"context"
	"fmt"

	"urgentry/internal/sourcemap"
)

// resolveSourceContext enhances parsed exception groups by applying source map
// resolution. For each frame whose minified location can be mapped through an
// uploaded source map artifact, the original filename, line number, and function
// name replace the minified values and the original minified location is stored
// in MappedFrom. Frames that cannot be resolved are left unchanged.
func resolveSourceContext(ctx context.Context, resolver *sourcemap.Resolver, projectID, release string, groups []exceptionGroup) []exceptionGroup {
	if resolver == nil || projectID == "" || release == "" || len(groups) == 0 {
		return groups
	}

	for gi := range groups {
		for fi := range groups[gi].Frames {
			frame := &groups[gi].Frames[fi]
			if frame.File == "" || frame.LineNo == 0 {
				continue
			}

			origFile, origLine, origFunc, err := resolver.Resolve(
				ctx, projectID, release, frame.File, frame.LineNo, frame.ColNo,
			)
			if err != nil || origFile == "" {
				// No source map available or resolution failed — keep original.
				continue
			}

			// Record the original minified location before overwriting.
			mapped := fmt.Sprintf("mapped from %s:%d", frame.File, frame.LineNo)
			if frame.ColNo > 0 {
				mapped = fmt.Sprintf("mapped from %s:%d:%d", frame.File, frame.LineNo, frame.ColNo)
			}
			frame.MappedFrom = mapped
			frame.File = origFile
			frame.LineNo = origLine
			if origFunc != "" {
				frame.Function = origFunc
			}
		}
	}

	return groups
}
