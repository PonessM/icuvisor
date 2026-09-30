package intervals

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestActivityStreamUnmarshalPreservesRawFields(t *testing.T) {
	t.Parallel()

	var got ActivityStream
	if err := json.Unmarshal([]byte(`{"type":"Power","name":"Watts","data":[250,260.5],"data2":[1,2],"valueTypeIsArray":true,"anomalies":[{"start_index":3,"end_index":5,"value":250,"valueEnd":260},{"start_index":7,"end_index":8,"value":270,"valueEnd":280}],"custom":true,"allNull":false,"extra":{"unit":"W"}}`), &got); err != nil {
		t.Fatalf("UnmarshalJSON() error = %v", err)
	}
	if got.Type != "Power" || got.Name != "Watts" || len(got.Data) != 2 || got.Data[1] != 260.5 || len(got.Data2) != 2 {
		t.Fatalf("typed stream = %+v", got)
	}
	if !got.ValueTypeIsArray || !got.Custom || got.AllNull || len(got.Anomalies) != 2 {
		t.Fatalf("flags/anomalies = %+v", got)
	}
	if got.Anomalies[0] != (ActivityStreamAnomaly{StartIndex: 3, EndIndex: 5, Value: 250, ValueEnd: 260}) {
		t.Fatalf("anomaly = %+v", got.Anomalies[0])
	}
	extra, ok := got.Raw["extra"].(map[string]any)
	if !ok || extra["unit"] != "W" {
		t.Fatalf("Raw extra = %#v, want preserved upstream fields", got.Raw["extra"])
	}
}

func TestGetActivityStreamsBuildsQuery(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/activity/a1/streams"; got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
		query := r.URL.Query()
		if got, want := query.Get("types"), "watts,heartrate"; got != want {
			t.Fatalf("types query = %q, want %q", got, want)
		}
		if got, want := query.Get("includeDefaults"), "true"; got != want {
			t.Fatalf("includeDefaults query = %q, want %q", got, want)
		}
		if got := query.Get("include_defaults"); got != "" {
			t.Fatalf("include_defaults query = %q, want absent", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"type":"watts","data":[1],"upstream_extra":"kept"}]`))
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, server.Client(), RetryConfig{})
	got, err := client.GetActivityStreams(context.Background(), ActivityStreamsParams{ActivityID: " a1 ", Types: []string{" watts ", "", "heartrate"}, IncludeDefaults: true})
	if err != nil {
		t.Fatalf("GetActivityStreams() error = %v", err)
	}
	if len(got) != 1 || got[0].Type != "watts" || got[0].Raw["upstream_extra"] != "kept" {
		t.Fatalf("streams = %#v", got)
	}
}

func TestGetActivityStreamsRequiresActivityID(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, "https://example.invalid", http.DefaultClient, RetryConfig{})
	_, err := client.GetActivityStreams(context.Background(), ActivityStreamsParams{ActivityID: " \t "})
	if err == nil || !strings.Contains(err.Error(), "activity ID is required") {
		t.Fatalf("GetActivityStreams() error = %v, want required activity ID", err)
	}
}

func TestGetActivityStreamsWrapsHTTPError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, server.Client(), RetryConfig{MaxAttempts: 1})
	_, err := client.GetActivityStreams(context.Background(), ActivityStreamsParams{ActivityID: "a1"})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("GetActivityStreams() error = %v, want ErrUnauthorized", err)
	}
	if !strings.Contains(err.Error(), "getting activity a1 streams") {
		t.Fatalf("GetActivityStreams() error = %q, want activity context", err.Error())
	}
}

func TestGetActivityStreamsHeartRateAliasesPreserveOtherKeysAndInput(t *testing.T) {
	t.Parallel()
	for _, alias := range []string{"heart_rate", "heartRate", "HeartRate", "HR", "hr", "heartrate"} {
		t.Run(alias, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.URL.Query().Get("types"); got != "heartrate,custom_channel,time" {
					t.Errorf("types = %q, want heartrate,custom_channel,time", got)
					http.Error(w, "unexpected types", http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`[{"type":"heartrate","data":[123]}]`))
			}))
			defer server.Close()
			client := newTestClient(t, server.URL, server.Client(), RetryConfig{})
			types := []string{" " + alias + " ", "custom_channel", "", " time "}
			original := append([]string(nil), types...)
			got, err := client.GetActivityStreams(context.Background(), ActivityStreamsParams{ActivityID: "a1", Types: types})
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].Type != "heartrate" || len(got[0].Data) != 1 || got[0].Data[0] != 123 {
				t.Fatalf("streams = %#v", got)
			}
			if !reflect.DeepEqual(types, original) {
				t.Fatalf("input types changed: got %#v, want %#v", types, original)
			}
		})
	}
}

func TestActivityStreamMalformedOptionalFieldsPreserveNumericChannels(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, raw string
		field     string
		wantData  []float64
	}{
		{"boolean", `{"type":"moving","data":[true,false]}`, "data", nil},
		{"mixed samples", `{"type":"watts","data":[200,"bad",250]}`, "data", nil},
		{"paired data", `{"type":"latlng","data":[1,2],"data2":["bad",3]}`, "data2", []float64{1, 2}},
		{"anomalies", `{"type":"watts","data":[200,250],"anomalies":[1]}`, "anomalies", []float64{200, 250}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stream ActivityStream
			if err := json.Unmarshal([]byte(tc.raw), &stream); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(stream.Data, tc.wantData) {
				t.Fatalf("samples = %#v, want %#v", stream.Data, tc.wantData)
			}
			if stream.Raw == nil {
				t.Fatal("raw evidence lost")
			}
			if !reflect.DeepEqual(stream.InvalidFields, []string{tc.field}) {
				t.Fatalf("invalid fields = %#v", stream.InvalidFields)
			}
			if stream.Data2 != nil || stream.Anomalies != nil {
				t.Fatalf("partially decoded slices retained: %#v", stream)
			}
		})
	}
}
