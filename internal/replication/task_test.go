package replication

import "testing"

func TestExtractTaskUID(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    int64
		wantErr bool
	}{
		{
			name: "document addition",
			body: `{"taskUid":42,"indexUid":"movies","status":"enqueued","type":"documentAdditionOrUpdate","enqueuedAt":"2024-01-01T00:00:00Z"}`,
			want: 42,
		},
		{
			name: "index creation",
			body: `{"taskUid":1,"indexUid":"movies","status":"enqueued","type":"indexCreation","enqueuedAt":"2024-01-01T00:00:00Z"}`,
			want: 1,
		},
		{
			name: "settings update",
			body: `{"taskUid":100,"indexUid":"movies","status":"enqueued","type":"settingsUpdate","enqueuedAt":"2024-01-01T00:00:00Z"}`,
			want: 100,
		},
		{
			name: "large taskUid",
			body: `{"taskUid":999999,"indexUid":"products","status":"enqueued","type":"documentDeletion","enqueuedAt":"2024-01-01T00:00:00Z"}`,
			want: 999999,
		},
		{
			name:    "invalid JSON",
			body:    `not json`,
			wantErr: true,
		},
		{
			name:    "empty object",
			body:    `{}`,
			wantErr: true,
		},
		{
			name:    "empty body",
			body:    ``,
			wantErr: true,
		},
		{
			name: "taskUid zero with status",
			body: `{"taskUid":0,"status":"enqueued"}`,
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExtractTaskUID([]byte(tt.body))
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got taskUid=%d", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got taskUid=%d, want %d", got, tt.want)
			}
		})
	}
}
