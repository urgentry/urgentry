package sourcemap

import "context"

// Resolver looks up source maps from the Store and applies the VLQ resolution.
type Resolver struct {
	Store Store
}

// Resolve looks up the source map for the given file in the given release,
// then resolves the minified line:col to the original source location.
// Returns zero values (not an error) when no source map is available.
func (r *Resolver) Resolve(ctx context.Context, projectID, release, filename string, line, col int) (string, int, string, error) {
	if r.Store == nil || release == "" || filename == "" {
		return "", 0, "", nil
	}

	// Try filename + ".map" first (standard convention).
	_, data, err := r.Store.LookupByName(ctx, projectID, release, filename+".map")
	if err != nil || data == nil {
		// Fall back to the exact name (maybe it IS the source map name).
		_, data, err = r.Store.LookupByName(ctx, projectID, release, filename)
		if err != nil || data == nil {
			return "", 0, "", nil
		}
	}

	return Resolve(data, line, col)
}
