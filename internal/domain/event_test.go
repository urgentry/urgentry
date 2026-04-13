package domain

import "testing"

func boolPtr(v bool) *bool { return &v }

func TestEventTitle(t *testing.T) {
	tests := []struct {
		name  string
		event Event
		want  string
	}{
		{
			name: "exception type and value",
			event: Event{
				Exception: &ExceptionList{
					Values: []Exception{
						{Type: "ValueError", Value: "invalid literal"},
					},
				},
			},
			want: "ValueError: invalid literal",
		},
		{
			name: "exception type only",
			event: Event{
				Exception: &ExceptionList{
					Values: []Exception{
						{Type: "NullPointerException"},
					},
				},
			},
			want: "NullPointerException",
		},
		{
			name: "exception value only",
			event: Event{
				Exception: &ExceptionList{
					Values: []Exception{
						{Value: "something went wrong"},
					},
				},
			},
			want: "something went wrong",
		},
		{
			name: "multiple exceptions uses last",
			event: Event{
				Exception: &ExceptionList{
					Values: []Exception{
						{Type: "CauseError", Value: "root cause"},
						{Type: "WrapperError", Value: "wrapped"},
					},
				},
			},
			want: "WrapperError: wrapped",
		},
		{
			name: "empty exception falls through to message",
			event: Event{
				Exception: &ExceptionList{Values: []Exception{{}}},
				Message:   "fallback message",
			},
			want: "fallback message",
		},
		{
			name: "message fallback",
			event: Event{
				Message: "short message",
			},
			want: "short message",
		},
		{
			name: "long message truncated at 100 chars",
			event: Event{
				Message: "AAAAAAAAAA" + "BBBBBBBBBB" + "CCCCCCCCCC" + "DDDDDDDDDD" +
					"EEEEEEEEEE" + "FFFFFFFFFF" + "GGGGGGGGGG" + "HHHHHHHHHH" +
					"IIIIIIIIII" + "JJJJJJJJJJ" + "KKKKKKKKKK",
			},
			want: "AAAAAAAAAA" + "BBBBBBBBBB" + "CCCCCCCCCC" + "DDDDDDDDDD" +
				"EEEEEEEEEE" + "FFFFFFFFFF" + "GGGGGGGGGG" + "HHHHHHHHHH" +
				"IIIIIIIIII" + "JJJJJJJJJJ",
		},
		{
			name:  "no title",
			event: Event{},
			want:  "<no title>",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.event.EventTitle(); got != tt.want {
				t.Errorf("EventTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEventCulprit(t *testing.T) {
	tests := []struct {
		name  string
		event Event
		want  string
	}{
		{
			name: "in-app frame with module and function",
			event: Event{
				Exception: &ExceptionList{
					Values: []Exception{
						{
							Stacktrace: &Stacktrace{
								Frames: []Frame{
									{Filename: "vendor.go", Function: "vendorFn", InApp: boolPtr(false)},
									{Module: "myapp.handlers", Function: "handleRequest", InApp: boolPtr(true)},
								},
							},
						},
					},
				},
			},
			want: "myapp.handlers in handleRequest",
		},
		{
			name: "in-app frame with filename and function",
			event: Event{
				Exception: &ExceptionList{
					Values: []Exception{
						{
							Stacktrace: &Stacktrace{
								Frames: []Frame{
									{Filename: "app.py", Function: "main", InApp: boolPtr(true)},
								},
							},
						},
					},
				},
			},
			want: "app.py in main",
		},
		{
			name: "in-app frame with function only",
			event: Event{
				Exception: &ExceptionList{
					Values: []Exception{
						{
							Stacktrace: &Stacktrace{
								Frames: []Frame{
									{Function: "doWork", InApp: boolPtr(true)},
								},
							},
						},
					},
				},
			},
			want: "doWork",
		},
		{
			name: "no in-app frames returns empty",
			event: Event{
				Exception: &ExceptionList{
					Values: []Exception{
						{
							Stacktrace: &Stacktrace{
								Frames: []Frame{
									{Module: "stdlib", Function: "run", InApp: boolPtr(false)},
								},
							},
						},
					},
				},
			},
			want: "",
		},
		{
			name: "multiple exceptions prefers last with in-app",
			event: Event{
				Exception: &ExceptionList{
					Values: []Exception{
						{
							Stacktrace: &Stacktrace{
								Frames: []Frame{
									{Module: "inner", Function: "cause", InApp: boolPtr(true)},
								},
							},
						},
						{
							Stacktrace: &Stacktrace{
								Frames: []Frame{
									{Module: "outer", Function: "wrap", InApp: boolPtr(true)},
								},
							},
						},
					},
				},
			},
			want: "outer in wrap",
		},
		{
			name:  "no exception returns empty",
			event: Event{},
			want:  "",
		},
		{
			name: "nil InApp pointer is not in-app",
			event: Event{
				Exception: &ExceptionList{
					Values: []Exception{
						{
							Stacktrace: &Stacktrace{
								Frames: []Frame{
									{Module: "pkg", Function: "fn"},
								},
							},
						},
					},
				},
			},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.event.EventCulprit(); got != tt.want {
				t.Errorf("EventCulprit() = %q, want %q", got, tt.want)
			}
		})
	}
}
