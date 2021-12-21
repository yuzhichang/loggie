package normalize

import (
	"reflect"
	"testing"

	"loggie.io/loggie/pkg/core/api"
	"loggie.io/loggie/pkg/core/event"
)

func TestFormatProcessor_Process(t *testing.T) {
	type fields struct {
		config *FormatConfig
	}
	type args struct {
		e api.Event
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   map[string]interface{}
	}{
		{
			name: "under root",
			fields: fields{config: &FormatConfig{
				Format: map[string]string{
					"a": formatBoolean,
					"b": formatInteger,
					"c": formatFloat,
				},
				UnderRoot: true,
			}},
			args: args{e: &event.DefaultEvent{
				H: map[string]interface{}{
					"a": "t",
					"b": "123",
					"c": "234.0",
					"d": "e",
				},
				B: nil,
			}},
			want: map[string]interface{}{
				"a": true,
				"b": int64(123),
				"c": 234.0,
				"d": "e",
			},
		},
		{
			name: "not under root",
			fields: fields{config: &FormatConfig{
				Format: map[string]string{
					"a": formatBoolean,
					"b": formatInteger,
					"c": formatFloat,
				},
				UnderRoot: false,
			}},
			args: args{e: &event.DefaultEvent{
				H: map[string]interface{}{
					SystemLogBody: map[string]interface{}{
						"a": "t",
						"b": "123",
						"c": "234.0",
						"d": "e",
					},
					"e": "f",
				},
				B: nil,
			}},
			want: map[string]interface{}{
				SystemLogBody: map[string]interface{}{
					"a": true,
					"b": int64(123),
					"c": 234.0,
					"d": "e",
				},
				"e": "f",
			},
		},
		{
			name: "nil format config",
			fields: fields{config: &FormatConfig{
				UnderRoot: true,
			}},
			args: args{e: &event.DefaultEvent{
				H: map[string]interface{}{
					"a": "t",
					"b": "123",
					"c": "234.0",
					"d": "e",
				},
				B: nil,
			}},
			want: map[string]interface{}{
				"a": "t",
				"b": "123",
				"c": "234.0",
				"d": "e",
			},
		},
		{
			name: "mismatch",
			fields: fields{config: &FormatConfig{
				UnderRoot: true,
				Format: map[string]string{
					"e": formatBoolean,
					"f": formatInteger,
					"g": formatFloat,
				},
			}},
			args: args{e: &event.DefaultEvent{
				H: map[string]interface{}{
					"a": "t",
					"b": "123",
					"c": "234.0",
					"d": "e",
				},
				B: nil,
			}},
			want: map[string]interface{}{
				"a": "t",
				"b": "123",
				"c": "234.0",
				"d": "e",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &FormatProcessor{
				config: tt.fields.config,
			}
			_ = p.Process(tt.args.e)
			if !reflect.DeepEqual(tt.want, tt.args.e.Header()) {
				t.Errorf("Process() got = %v, want=%v", tt.args.e.Header(), tt.want)
			}
		})
	}
}
