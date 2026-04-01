package issue

import (
	"context"

	"urgentry/internal/nativesym"
	"urgentry/internal/normalize"
)

// ApplyEventResolvers enriches one normalized event with source-map,
// ProGuard, and native symbol data. It returns true when any frame changed.
func ApplyEventResolvers(ctx context.Context, projectID string, evt *normalize.Event, sourceMaps SourceMapResolver, proGuard ProGuardResolver, native NativeSymbolResolver) bool {
	if evt == nil || evt.Exception == nil {
		return false
	}

	changed := false

	if sourceMaps != nil && evt.Release != "" {
		for i, exc := range evt.Exception.Values {
			if exc.Stacktrace == nil {
				continue
			}
			for j, frame := range exc.Stacktrace.Frames {
				if frame.Lineno <= 0 {
					continue
				}
				origFile, origLine, origFunc, err := sourceMaps.Resolve(ctx, projectID, evt.Release, frame.Filename, frame.Lineno, frame.Colno)
				if err != nil || origFile == "" {
					continue
				}
				if frame.AbsPath == "" {
					evt.Exception.Values[i].Stacktrace.Frames[j].AbsPath = frame.Filename
				}
				evt.Exception.Values[i].Stacktrace.Frames[j].Filename = origFile
				evt.Exception.Values[i].Stacktrace.Frames[j].Lineno = origLine
				if origFunc != "" {
					evt.Exception.Values[i].Stacktrace.Frames[j].Function = origFunc
				}
				changed = true
			}
		}
	}

	if proGuard != nil && evt.Release != "" && (evt.Platform == "java" || evt.Platform == "android") {
		for i, exc := range evt.Exception.Values {
			if exc.Stacktrace == nil {
				continue
			}
			for j, frame := range exc.Stacktrace.Frames {
				if frame.Module == "" {
					continue
				}
				origModule, origFile, origFunc, origLine, err := proGuard.Resolve(ctx, projectID, evt.Release, frame.Module, frame.Function, frame.Lineno)
				if err != nil || origModule == "" {
					continue
				}
				if frame.AbsPath == "" {
					if frame.Module != "" {
						evt.Exception.Values[i].Stacktrace.Frames[j].AbsPath = frame.Module
					} else {
						evt.Exception.Values[i].Stacktrace.Frames[j].AbsPath = frame.Filename
					}
				}
				evt.Exception.Values[i].Stacktrace.Frames[j].Module = origModule
				if origFile != "" {
					evt.Exception.Values[i].Stacktrace.Frames[j].Filename = origFile
				}
				if origFunc != "" {
					evt.Exception.Values[i].Stacktrace.Frames[j].Function = origFunc
				}
				if origLine > 0 {
					evt.Exception.Values[i].Stacktrace.Frames[j].Lineno = origLine
				}
				changed = true
			}
		}
	}

	if native != nil && evt.Release != "" {
		for i, exc := range evt.Exception.Values {
			if exc.Stacktrace == nil {
				continue
			}
			for j, frame := range exc.Stacktrace.Frames {
				if frame.InstructionAddr == "" {
					continue
				}
				result, err := native.Resolve(ctx, nativesym.LookupRequest{
					ProjectID:       projectID,
					ReleaseVersion:  evt.Release,
					DebugID:         frame.DebugID,
					CodeID:          frame.Package,
					ModuleName:      frame.Module,
					InstructionAddr: frame.InstructionAddr,
				})
				if err != nil || (result.Module == "" && result.File == "" && result.Function == "" && result.Line == 0) {
					continue
				}
				if frame.AbsPath == "" {
					switch {
					case frame.Package != "":
						evt.Exception.Values[i].Stacktrace.Frames[j].AbsPath = frame.Package
					case frame.Module != "":
						evt.Exception.Values[i].Stacktrace.Frames[j].AbsPath = frame.Module
					}
				}
				if result.Module != "" {
					evt.Exception.Values[i].Stacktrace.Frames[j].Module = result.Module
				}
				if result.File != "" {
					evt.Exception.Values[i].Stacktrace.Frames[j].Filename = result.File
				}
				if result.Function != "" {
					evt.Exception.Values[i].Stacktrace.Frames[j].Function = result.Function
				}
				if result.Line > 0 {
					evt.Exception.Values[i].Stacktrace.Frames[j].Lineno = result.Line
				}
				changed = true
			}
		}
	}

	return changed
}
