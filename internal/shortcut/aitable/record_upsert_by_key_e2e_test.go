// Copyright 2026 Alibaba Group
// SPDX-License-Identifier: Apache-2.0

package aitable

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	apperrors "github.com/DingTalk-Real-AI/dingtalk-workspace-cli/internal/errors"
	"github.com/DingTalk-Real-AI/dingtalk-workspace-cli/internal/helpers"
	"github.com/DingTalk-Real-AI/dingtalk-workspace-cli/internal/shortcut"
	"github.com/DingTalk-Real-AI/dingtalk-workspace-cli/pkg/edition"
	"github.com/spf13/cobra"
)

type upsertByKeyStep struct {
	text string
	err  error
}

type upsertByKeyCaller struct {
	steps  []upsertByKeyStep
	calls  []aitableURLCall
	dryRun bool
	callFn func(index int, product, tool string, args map[string]any) (string, error)
}

func (c *upsertByKeyCaller) CallTool(_ context.Context, product, tool string, args map[string]any) (*edition.ToolResult, error) {
	return c.call(product, tool, args)
}

func (c *upsertByKeyCaller) CallReadTool(_ context.Context, product, tool string, args map[string]any) (*edition.ToolResult, error) {
	return c.call(product, tool, args)
}

func (c *upsertByKeyCaller) call(product, tool string, args map[string]any) (*edition.ToolResult, error) {
	cloned := make(map[string]any, len(args))
	for key, value := range args {
		cloned[key] = value
	}
	c.calls = append(c.calls, aitableURLCall{product: product, tool: tool, args: cloned})
	index := len(c.calls) - 1
	if c.callFn != nil {
		text, err := c.callFn(index, product, tool, args)
		if err != nil {
			return nil, err
		}
		return urlResult(text), nil
	}
	if index >= len(c.steps) {
		return nil, errors.New("unexpected upsert-by-key call")
	}
	step := c.steps[index]
	if step.err != nil {
		return nil, step.err
	}
	return urlResult(step.text), nil
}

func (*upsertByKeyCaller) Format() string { return "json" }
func (c *upsertByKeyCaller) DryRun() bool { return c.dryRun }
func (*upsertByKeyCaller) Fields() string { return "" }
func (*upsertByKeyCaller) JQ() string     { return "" }

func runUpsertByKeyCLI(t *testing.T, caller *upsertByKeyCaller, extra ...string) (string, error) {
	t.Helper()
	helpers.InitDepsForTest(t, caller)
	root := &cobra.Command{Use: "dws", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().Bool("yes", false, "")
	root.PersistentFlags().Bool("dry-run", false, "")
	root.PersistentFlags().String("format", "json", "")
	root.AddCommand(shortcut.Commands()...)
	stdout := &bytes.Buffer{}
	root.SetOut(stdout)
	root.SetErr(&bytes.Buffer{})
	args := []string{
		"aitable", "+record-upsert-by-key",
		"--base-id", "base", "--table-id", "table",
		"--key-field-id", "fldKey", "--key-value", "TASK-1",
		"--cells", `{"fldStatus":"进行中"}`,
		"--yes",
	}
	args = append(args, extra...)
	root.SetArgs(args)
	err := root.Execute()
	return stdout.String(), err
}

func TestCrossPlatformCoverageRecordUpsertByKeyCreateE2E(t *testing.T) {
	caller := &upsertByKeyCaller{steps: []upsertByKeyStep{
		{text: `{"data":{"records":[]}}`},
		{text: `{"data":{"records":[{"recordId":"r1"}]}}`},
		{text: `{"data":{"records":[{"recordId":"r1","cells":{"fldKey":"TASK-1","fldStatus":"进行中"}}]}}`},
	}}
	out, err := runUpsertByKeyCLI(t, caller)
	if err != nil {
		t.Fatalf("create upsert error = %v", err)
	}
	for _, want := range []string{`"status": "success"`, `"action": "create"`, `"recordId": "r1"`, `"status": "verified"`} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Fatalf("create output missing %s:\n%s", want, out)
		}
	}
	if len(caller.calls) != 3 || caller.calls[1].tool != "create_records" || caller.calls[2].tool != "query_records" {
		t.Fatalf("create call flow = %#v", caller.calls)
	}
}

func TestCrossPlatformCoverageRecordUpsertByKeyUpdateE2E(t *testing.T) {
	caller := &upsertByKeyCaller{steps: []upsertByKeyStep{
		{text: `{"records":[{"recordId":"r1","cells":{"fldKey":"TASK-1","fldStatus":"旧"}}]}`},
		{text: `{"updatedRecords":[{"recordId":"r1"}]}`},
		{text: `{"records":[{"recordId":"r1","cells":{"fldKey":"TASK-1","fldStatus":"进行中"}}]}`},
	}}
	out, err := runUpsertByKeyCLI(t, caller)
	if err != nil || !bytes.Contains([]byte(out), []byte(`"action": "update"`)) {
		t.Fatalf("update upsert = output:%q err:%v", out, err)
	}
	if caller.calls[1].tool != "update_records" {
		t.Fatalf("update tool = %s", caller.calls[1].tool)
	}
}

func TestCrossPlatformCoverageRecordUpsertByKeyAmbiguousStopsBeforeWriteE2E(t *testing.T) {
	caller := &upsertByKeyCaller{steps: []upsertByKeyStep{{text: `{"records":[
		{"recordId":"r1","cells":{"fldKey":"TASK-1"}},
		{"recordId":"r2","cells":{"fldKey":"TASK-1"}}
	]}`}}}
	out, err := runUpsertByKeyCLI(t, caller)
	if err == nil || out != "" || len(caller.calls) != 1 {
		t.Fatalf("ambiguous upsert = output:%q err:%v calls:%#v", out, err, caller.calls)
	}
	var typed *apperrors.Error
	if !errors.As(err, &typed) || typed.Reason != "target_ambiguous" || typed.ExecutionStarted == nil || *typed.ExecutionStarted {
		t.Fatalf("ambiguous error = %#v", err)
	}
}

func TestCrossPlatformCoverageRecordUpsertByKeyDryRunReadsAndPlansE2E(t *testing.T) {
	caller := &upsertByKeyCaller{dryRun: true, steps: []upsertByKeyStep{{text: `{"records":[]}`}}}
	out, err := runUpsertByKeyCLI(t, caller, "--dry-run")
	if err != nil {
		t.Fatalf("dry-run upsert error = %v", err)
	}
	if len(caller.calls) != 1 || caller.calls[0].tool != "query_records" {
		t.Fatalf("dry-run calls = %#v", caller.calls)
	}
	for _, want := range []string{`"status": "planned"`, `"executed": false`, `"tool": "create_records"`} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Fatalf("dry-run output missing %s: %s", want, out)
		}
	}
}

func TestCrossPlatformCoverageRecordUpsertByKeyWriteReplyNeedsReadBackE2E(t *testing.T) {
	t.Run("empty write recovered by proven state", func(t *testing.T) {
		caller := &upsertByKeyCaller{steps: []upsertByKeyStep{
			{text: `{"records":[]}`},
			{text: ""},
			{text: `{"records":[{"recordId":"r1","cells":{"fldKey":"TASK-1","fldStatus":"进行中"}}]}`},
		}}
		out, err := runUpsertByKeyCLI(t, caller)
		if err != nil || !bytes.Contains([]byte(out), []byte(`"status": "recovered"`)) || !bytes.Contains([]byte(out), []byte("proven by read-back")) {
			t.Fatalf("recovered write = output:%q err:%v", out, err)
		}
	})

	t.Run("empty write and absent state is unknown", func(t *testing.T) {
		caller := &upsertByKeyCaller{steps: []upsertByKeyStep{
			{text: `{"records":[]}`},
			{text: ""},
			{text: `{"records":[]}`},
		}}
		out, err := runUpsertByKeyCLI(t, caller)
		if err == nil || out != "" {
			t.Fatalf("unknown write = output:%q err:%v", out, err)
		}
		var typed *apperrors.Error
		if !errors.As(err, &typed) || typed.Reason != "aitable_composite_unknown" || typed.Retryable {
			t.Fatalf("unknown write error = %#v", err)
		}
		if typed.Details == nil || !strings.Contains(fmt.Sprint(typed.Details), "query the unique key again") {
			t.Fatalf("unknown create is missing safe recovery checkpoint: %#v", typed.Details)
		}
	})

	t.Run("unknown update remains retryable because record id is known", func(t *testing.T) {
		caller := &upsertByKeyCaller{steps: []upsertByKeyStep{
			{text: `{"records":[{"recordId":"r1","cells":{"fldKey":"TASK-1","fldStatus":"旧"}}]}`},
			{text: ""},
			{text: `{"records":[{"recordId":"r1","cells":{"fldKey":"TASK-1","fldStatus":"旧"}}]}`},
		}}
		out, err := runUpsertByKeyCLI(t, caller)
		if err == nil || out != "" {
			t.Fatalf("unknown update = output:%q err:%v", out, err)
		}
		var typed *apperrors.Error
		if !errors.As(err, &typed) || typed.Reason != "aitable_composite_unknown" || !typed.Retryable {
			t.Fatalf("unknown update error = %#v", err)
		}
	})
}

func TestCrossPlatformCoverageRecordUpsertByKeyVerificationMismatchFailsE2E(t *testing.T) {
	caller := &upsertByKeyCaller{steps: []upsertByKeyStep{
		{text: `{"records":[]}`},
		{text: `{"createdRecords":[{"recordId":"r1"}]}`},
		{text: `{"records":[{"recordId":"r1","cells":{"fldKey":"TASK-1","fldStatus":"错误值"}}]}`},
	}}
	out, err := runUpsertByKeyCLI(t, caller)
	if err == nil || out != "" {
		t.Fatalf("verification mismatch = output:%q err:%v", out, err)
	}
}

func runRecordBatchCLI(t *testing.T, caller *upsertByKeyCaller, command string, records []map[string]any, extra ...string) (string, error) {
	t.Helper()
	helpers.InitDepsForTest(t, caller)
	root := &cobra.Command{Use: "dws", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().Bool("yes", false, "")
	root.PersistentFlags().Bool("dry-run", false, "")
	root.PersistentFlags().String("format", "json", "")
	root.AddCommand(shortcut.Commands()...)
	stdout := &bytes.Buffer{}
	root.SetOut(stdout)
	root.SetErr(&bytes.Buffer{})
	raw, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"aitable", command, "--base-id", "base", "--table-id", "table", "--records", string(raw), "--yes"}
	args = append(args, extra...)
	root.SetArgs(args)
	err = root.Execute()
	return stdout.String(), err
}

func updateFixtureRecords(start, count int, status string) []map[string]any {
	records := make([]map[string]any, 0, count)
	for index := start; index < start+count; index++ {
		records = append(records, map[string]any{
			"recordId": fmt.Sprintf("r%03d", index),
			"cells":    map[string]any{"fldStatus": status},
		})
	}
	return records
}

func recordListJSON(t *testing.T, records []map[string]any) string {
	t.Helper()
	items := make([]any, 0, len(records))
	for _, record := range records {
		items = append(items, record)
	}
	raw, err := json.Marshal(map[string]any{"data": map[string]any{"records": items}})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func pagedRecordQueryResponse(t *testing.T, records []map[string]any, args map[string]any) string {
	t.Helper()
	limit, ok := args["limit"].(int)
	if !ok || limit < 1 || limit > recordQueryServicePageSize {
		t.Fatalf("query_records limit = %#v, want 1..%d", args["limit"], recordQueryServicePageSize)
	}
	offset := 0
	if cursor, _ := args["cursor"].(string); cursor != "" {
		if _, err := fmt.Sscanf(cursor, "offset-%d", &offset); err != nil {
			t.Fatalf("invalid test cursor %q: %v", cursor, err)
		}
	}
	end := minInt(offset+limit, len(records))
	pageRecords := records[offset:end]
	data := map[string]any{"records": pageRecords, "hasMore": end < len(records)}
	if end < len(records) {
		data["nextCursor"] = fmt.Sprintf("offset-%d", end)
	}
	raw, err := json.Marshal(map[string]any{
		"success": true,
		"hasMore": end < len(records),
		"page":    offset/recordQueryServicePageSize + 1,
		"size":    limit,
		"data":    data,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func runRecordQueryShortcutCLI(t *testing.T, caller *upsertByKeyCaller, limit int) (map[string]any, error) {
	t.Helper()
	helpers.InitDepsForTest(t, caller)
	root := &cobra.Command{Use: "dws", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().Bool("yes", false, "")
	root.PersistentFlags().Bool("dry-run", false, "")
	root.PersistentFlags().String("format", "json", "")
	root.AddCommand(shortcut.Commands()...)
	stdout := &bytes.Buffer{}
	root.SetOut(stdout)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"aitable", "+record-query", "--base-id", "base", "--table-id", "table", "--limit", fmt.Sprint(limit)})
	err := root.Execute()
	if stdout.Len() == 0 {
		return nil, err
	}
	var payload map[string]any
	if decodeErr := json.Unmarshal(stdout.Bytes(), &payload); decodeErr != nil {
		t.Fatalf("decode record query output %q: %v", stdout.String(), decodeErr)
	}
	return payload, err
}

func TestCrossPlatformCoverageRecordQueryServicePageBoundariesE2E(t *testing.T) {
	for _, size := range []int{20, 21, 22, 100} {
		t.Run(fmt.Sprintf("size_%d", size), func(t *testing.T) {
			records := updateFixtureRecords(0, size, "可见")
			caller := &upsertByKeyCaller{}
			caller.callFn = func(_ int, _, tool string, args map[string]any) (string, error) {
				if tool != "query_records" {
					return "", fmt.Errorf("unexpected tool %s", tool)
				}
				return pagedRecordQueryResponse(t, records, args), nil
			}
			payload, err := runRecordQueryShortcutCLI(t, caller, size)
			if err != nil {
				t.Fatalf("record query size %d: %v", size, err)
			}
			data, ok := payload["data"].(map[string]any)
			if !ok || payload["success"] != true || data["hasMore"] != false || len(data["records"].([]any)) != size {
				t.Fatalf("record query size %d payload = %#v", size, payload)
			}
			wantCalls := (size + recordQueryServicePageSize - 1) / recordQueryServicePageSize
			if len(caller.calls) != wantCalls || data["page"] != float64(wantCalls) || data["size"] != float64(size) {
				t.Fatalf("record query size %d calls=%d payload=%#v, want calls=%d", size, len(caller.calls), payload, wantCalls)
			}
		})
	}
}

func TestCrossPlatformCoverageRecordQueryFailureAndContinuationBranches(t *testing.T) {
	t.Run("non-positive window limit", func(t *testing.T) {
		caller := &upsertByKeyCaller{}
		helpers.InitDepsForTest(t, caller)
		rt := shortcut.RuntimeContextForTest(&cobra.Command{Use: "query"}, RecordQuery)
		if _, err := queryRecordWindow(rt, nil, 0); err == nil {
			t.Fatal("non-positive record query window limit accepted")
		}
	})

	t.Run("initial cursor cycle", func(t *testing.T) {
		caller := &upsertByKeyCaller{callFn: func(_ int, _, tool string, _ map[string]any) (string, error) {
			if tool != "query_records" {
				return "", fmt.Errorf("unexpected tool %s", tool)
			}
			return `{"success":true,"data":{"records":[{"recordId":"r1"}],"hasMore":true,"nextCursor":"seed"}}`, nil
		}}
		helpers.InitDepsForTest(t, caller)
		rt := shortcut.RuntimeContextForTest(&cobra.Command{Use: "query"}, RecordQuery)
		if _, err := queryRecordWindow(rt, map[string]any{"cursor": " seed "}, 2); err == nil || !strings.Contains(err.Error(), "cursor cycle") {
			t.Fatalf("initial cursor cycle error = %v", err)
		}
	})

	t.Run("strict explicit empty shape", func(t *testing.T) {
		for _, data := range []map[string]any{
			{"success": true, "status": "success", "data": map[string]any{"unexpected": true}},
			{"success": true, "status": "success", "data": map[string]any{}, "error": "bad"},
		} {
			if explicitEmptyRecordQuery(data) {
				t.Fatalf("invalid empty-query envelope accepted: %#v", data)
			}
		}
	})

	t.Run("explicit empty page", func(t *testing.T) {
		caller := &upsertByKeyCaller{callFn: func(_ int, _, _ string, _ map[string]any) (string, error) {
			return `{"success":true,"status":"success","error":{},"data":{}}`, nil
		}}
		helpers.InitDepsForTest(t, caller)
		rt := shortcut.RuntimeContextForTest(&cobra.Command{Use: "query"}, RecordQuery)
		window, err := queryRecordWindow(rt, map[string]any{}, 1)
		if err != nil || len(window.Records) != 0 || window.Pages != 1 || window.HasMore || window.NextCursor != "" {
			t.Fatalf("explicit empty window=%#v err=%v", window, err)
		}
	})

	t.Run("missing records collection", func(t *testing.T) {
		caller := &upsertByKeyCaller{callFn: func(_ int, _, _ string, _ map[string]any) (string, error) {
			return `{}`, nil
		}}
		helpers.InitDepsForTest(t, caller)
		rt := shortcut.RuntimeContextForTest(&cobra.Command{Use: "query"}, RecordQuery)
		if _, err := queryRecordWindow(rt, map[string]any{}, 1); err == nil || !strings.Contains(err.Error(), "missing the records collection") {
			t.Fatalf("missing records error = %v", err)
		}
	})

	t.Run("invalid shortcut limit", func(t *testing.T) {
		caller := &upsertByKeyCaller{}
		helpers.InitDepsForTest(t, caller)
		cmd := &cobra.Command{Use: "query"}
		cmd.Flags().Int("limit", 0, "")
		if err := cmd.Flags().Set("limit", "0"); err != nil {
			t.Fatal(err)
		}
		rt := shortcut.RuntimeContextForTest(cmd, RecordQuery)
		if err := executeRecordQuery(rt, map[string]any{}); err == nil {
			t.Fatal("invalid shortcut limit accepted")
		}
	})

	t.Run("shortcut query failure", func(t *testing.T) {
		caller := &upsertByKeyCaller{callFn: func(_ int, _, _ string, _ map[string]any) (string, error) {
			return "", errors.New("query unavailable")
		}}
		helpers.InitDepsForTest(t, caller)
		rt := shortcut.RuntimeContextForTest(&cobra.Command{Use: "query"}, RecordQuery)
		if err := executeRecordQuery(rt, map[string]any{}); err == nil || !strings.Contains(err.Error(), "query unavailable") {
			t.Fatalf("shortcut query failure = %v", err)
		}
	})

	t.Run("bounded continuation is published", func(t *testing.T) {
		records := updateFixtureRecords(0, 21, "visible")
		caller := &upsertByKeyCaller{callFn: func(_ int, _, tool string, args map[string]any) (string, error) {
			if tool != "query_records" {
				return "", fmt.Errorf("unexpected tool %s", tool)
			}
			return pagedRecordQueryResponse(t, records, args), nil
		}}
		payload, err := runRecordQueryShortcutCLI(t, caller, 20)
		data, _ := payload["data"].(map[string]any)
		if err != nil || data["hasMore"] != true || data["nextCursor"] != "offset-20" {
			t.Fatalf("bounded continuation payload=%#v err=%v", payload, err)
		}
	})

	t.Run("delete readback rejects continuation", func(t *testing.T) {
		caller := &upsertByKeyCaller{callFn: func(_ int, _, tool string, _ map[string]any) (string, error) {
			if tool == "query_records" {
				return `{"success":true,"data":{"records":[],"hasMore":true,"nextCursor":"unexpected"}}`, nil
			}
			return `{"deletedCount":1}`, nil
		}}
		if _, err := runRecordDeleteCLI(t, caller, []string{"r1"}); err == nil || len(caller.calls) != 2 || caller.calls[1].tool != "query_records" {
			t.Fatalf("delete continuation error = %v calls=%#v", err, caller.calls)
		}
	})
}

func TestCrossPlatformCoverageRecordWriteReadbackUsesStableServicePagesE2E(t *testing.T) {
	for _, operation := range []string{"update", "upsert", "delete"} {
		for _, size := range []int{21, 22, 100} {
			t.Run(fmt.Sprintf("%s_%d", operation, size), func(t *testing.T) {
				records := updateFixtureRecords(0, size, "完成")
				caller := &upsertByKeyCaller{}
				queryCalls := 0
				caller.callFn = func(_ int, _, tool string, args map[string]any) (string, error) {
					if tool != "query_records" {
						return `{"success":true}`, nil
					}
					queryCalls++
					if operation == "delete" {
						ids := args["recordIds"].([]string)
						empty := make([]map[string]any, len(ids))
						response := pagedRecordQueryResponse(t, empty, args)
						var payload map[string]any
						if err := json.Unmarshal([]byte(response), &payload); err != nil {
							t.Fatal(err)
						}
						page := payload["data"].(map[string]any)
						page["records"] = []any{}
						raw, _ := json.Marshal(payload)
						return string(raw), nil
					}
					response := pagedRecordQueryResponse(t, records, args)
					var payload map[string]any
					if err := json.Unmarshal([]byte(response), &payload); err != nil {
						t.Fatal(err)
					}
					page := payload["data"].(map[string]any)
					if len(page["records"].([]any))+recordQueryServicePageSize*(queryCalls-1) >= size {
						// The live service can retain a continuation after it has
						// returned every requested record ID. Exact-ID verification
						// must use ID coverage, not this advisory bit.
						page["hasMore"] = true
						page["nextCursor"] = "ignored-after-complete-id-set"
						payload["hasMore"] = true
					}
					raw, _ := json.Marshal(payload)
					return string(raw), nil
				}

				var out string
				var err error
				if operation == "delete" {
					out, err = runRecordDeleteCLI(t, caller, recordIDs(records))
				} else {
					out, err = runRecordBatchCLI(t, caller, "+record-"+operation, records)
				}
				if err != nil || out == "" {
					t.Fatalf("%s size %d false negative: output=%q err=%v", operation, size, out, err)
				}
				wantQueries := (size + recordQueryServicePageSize - 1) / recordQueryServicePageSize
				if queryCalls != wantQueries {
					t.Fatalf("%s size %d query calls=%d, want %d", operation, size, queryCalls, wantQueries)
				}
			})
		}
	}
}

func TestCrossPlatformCoverageRecordUpdateAutoChunksAndVerifiesE2E(t *testing.T) {
	records := updateFixtureRecords(0, 101, "完成")
	caller := &upsertByKeyCaller{steps: []upsertByKeyStep{
		{text: `{"updatedCount":100}`},
		{text: recordListJSON(t, records[:100])},
		{text: `{"updatedCount":1}`},
		{text: recordListJSON(t, records[100:])},
	}}
	out, err := runRecordBatchCLI(t, caller, "+record-update", records)
	if err != nil {
		t.Fatalf("record update batches error = %v", err)
	}
	for _, want := range []string{`"processedCount": 101`, `"batchCount": 2`, `"verifiedCount": 101`} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Fatalf("batch output missing %s: %s", want, out)
		}
	}
	if len(caller.calls) != 4 || caller.calls[0].tool != "update_records" || caller.calls[1].tool != "query_records" || caller.calls[2].tool != "update_records" {
		t.Fatalf("batch call sequence = %#v", caller.calls)
	}
	firstBatch := caller.calls[0].args["records"].([]any)
	secondBatch := caller.calls[2].args["records"].([]any)
	if len(firstBatch) != 100 || len(secondBatch) != 1 {
		t.Fatalf("batch sizes = %d/%d", len(firstBatch), len(secondBatch))
	}
}

func TestCrossPlatformCoverageRecordUpdatePartialStopsWithCheckpointE2E(t *testing.T) {
	records := updateFixtureRecords(0, 101, "完成")
	caller := &upsertByKeyCaller{steps: []upsertByKeyStep{
		{text: `{"updatedCount":100}`},
		{text: recordListJSON(t, records[:100])},
		{err: errors.New("connection reset after send")},
		{text: `{"records":[]}`},
	}}
	out, err := runRecordBatchCLI(t, caller, "+record-update", records)
	if err == nil || out != "" {
		t.Fatalf("partial record update = output:%q err:%v", out, err)
	}
	var typed *apperrors.Error
	if !errors.As(err, &typed) || typed.Reason != "aitable_composite_partial_success" {
		t.Fatalf("partial error = %#v", err)
	}
	result := typed.Details["result"].(compositeResult)
	if result.CompletedCount != 100 || result.Checkpoint["nextOffset"] != 100 {
		t.Fatalf("partial result = %#v", result)
	}
}

func TestCrossPlatformCoverageRecordUpdateEmptyWriteReplyRecoveredOnlyByReadBackE2E(t *testing.T) {
	records := updateFixtureRecords(0, 1, "完成")
	caller := &upsertByKeyCaller{steps: []upsertByKeyStep{
		{text: ""},
		{text: recordListJSON(t, records)},
	}}
	out, err := runRecordBatchCLI(t, caller, "+record-update", records)
	if err != nil || !bytes.Contains([]byte(out), []byte(`"status": "recovered"`)) {
		t.Fatalf("empty write reply recovery = output:%q err:%v", out, err)
	}
}

func TestCrossPlatformCoverageRecordUpsertRequiresCreatedIDsAndReadBackE2E(t *testing.T) {
	records := []map[string]any{
		{"recordId": "existing", "cells": map[string]any{"fldStatus": "完成"}},
		{"cells": map[string]any{"fldKey": "new"}},
	}
	caller := &upsertByKeyCaller{steps: []upsertByKeyStep{
		{text: `{"data":{"createdRecords":[{"recordId":"created"}]}}`},
		{text: recordListJSON(t, []map[string]any{records[0]})},
		{text: recordListJSON(t, []map[string]any{{"recordId": "created", "cells": map[string]any{"fldKey": "new"}}})},
	}}
	out, err := runRecordBatchCLI(t, caller, "+record-upsert", records)
	if err != nil || !bytes.Contains([]byte(out), []byte(`"verifiedCount": 2`)) {
		t.Fatalf("mixed upsert = output:%q err:%v", out, err)
	}

	caller = &upsertByKeyCaller{steps: []upsertByKeyStep{{text: `{"success":true}`}}}
	out, err = runRecordBatchCLI(t, caller, "+record-upsert", []map[string]any{{"cells": map[string]any{"fldKey": "new"}}})
	if err == nil || out != "" {
		t.Fatalf("upsert without created IDs = output:%q err:%v", out, err)
	}
	var typed *apperrors.Error
	if !errors.As(err, &typed) || typed.Retryable {
		t.Fatalf("upsert create with unknown effect must not be blindly retryable: %#v", err)
	}
}

func TestCrossPlatformCoverageRecordBatchDryRunPlansWithoutWritesE2E(t *testing.T) {
	records := updateFixtureRecords(0, 101, "完成")
	caller := &upsertByKeyCaller{dryRun: true}
	out, err := runRecordBatchCLI(t, caller, "+record-update", records, "--dry-run")
	if err != nil || len(caller.calls) != 0 {
		t.Fatalf("batch dry-run = output:%q err:%v calls:%#v", out, err, caller.calls)
	}
	if !bytes.Contains([]byte(out), []byte(`"status": "planned"`)) || !bytes.Contains([]byte(out), []byte(`"count": 100`)) || !bytes.Contains([]byte(out), []byte(`"count": 1`)) {
		t.Fatalf("batch dry-run output = %s", out)
	}
}

func runRecordDeleteCLI(t *testing.T, caller *upsertByKeyCaller, ids []string, extra ...string) (string, error) {
	t.Helper()
	helpers.InitDepsForTest(t, caller)
	root := &cobra.Command{Use: "dws", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().Bool("yes", false, "")
	root.PersistentFlags().Bool("dry-run", false, "")
	root.PersistentFlags().String("format", "json", "")
	root.AddCommand(shortcut.Commands()...)
	stdout := &bytes.Buffer{}
	root.SetOut(stdout)
	root.SetErr(&bytes.Buffer{})
	args := []string{"aitable", "+record-delete", "--base-id", "base", "--table-id", "table", "--record-ids", strings.Join(ids, ","), "--yes"}
	args = append(args, extra...)
	root.SetArgs(args)
	err := root.Execute()
	return stdout.String(), err
}

func recordIDFixtures(count int) []string {
	ids := make([]string, 0, count)
	for index := 0; index < count; index++ {
		ids = append(ids, fmt.Sprintf("r%03d", index))
	}
	return ids
}

func TestCrossPlatformCoverageRecordDeleteAutoChunksAndProvesAbsenceE2E(t *testing.T) {
	ids := recordIDFixtures(101)
	caller := &upsertByKeyCaller{steps: []upsertByKeyStep{
		{text: `{"deletedCount":100}`},
		{text: `{"data":{"records":[]}}`},
		{text: `{"data":{"records":[]}}`},
		{text: `{"data":{"records":[]}}`},
		{text: `{"data":{"records":[]}}`},
		{text: `{"data":{"records":[]}}`},
		{text: `{"deletedCount":1}`},
		{text: `{"data":{"records":[]}}`},
	}}
	out, err := runRecordDeleteCLI(t, caller, ids)
	if err != nil {
		t.Fatalf("record delete batches error = %v", err)
	}
	for _, want := range []string{`"deletedCount": 101`, `"batchCount": 2`, `"verifiedCount": 101`, `"status": "verified_absent"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("delete output missing %s: %s", want, out)
		}
	}
	if len(caller.calls) != 8 || caller.calls[0].tool != "delete_records" || caller.calls[1].tool != "query_records" || caller.calls[6].tool != "delete_records" {
		t.Fatalf("delete call sequence = %#v", caller.calls)
	}
}

func TestCrossPlatformCoverageRecordDeleteEmptyReplyRecoveredOnlyByAbsenceE2E(t *testing.T) {
	t.Run("legal empty collection proves deletion", func(t *testing.T) {
		caller := &upsertByKeyCaller{steps: []upsertByKeyStep{{text: ""}, {text: `{"records":[]}`}}}
		out, err := runRecordDeleteCLI(t, caller, []string{"r1"})
		if err != nil || !strings.Contains(out, `"status": "recovered"`) {
			t.Fatalf("delete recovered = output:%q err:%v", out, err)
		}
	})

	t.Run("explicit service success with empty data proves deletion", func(t *testing.T) {
		caller := &upsertByKeyCaller{steps: []upsertByKeyStep{
			{text: `{"deletedCount":1}`},
			{text: `{"success":true,"status":"success","error":{},"data":{}}`},
		}}
		out, err := runRecordDeleteCLI(t, caller, []string{"r1"})
		if err != nil || !strings.Contains(out, `"status": "verified_absent"`) {
			t.Fatalf("delete explicit empty success = output:%q err:%v", out, err)
		}
	})

	t.Run("missing records contract is unknown", func(t *testing.T) {
		caller := &upsertByKeyCaller{steps: []upsertByKeyStep{{text: `{"deletedCount":1}`}, {text: `{"data":{}}`}}}
		out, err := runRecordDeleteCLI(t, caller, []string{"r1"})
		if err == nil || out != "" {
			t.Fatalf("delete missing records = output:%q err:%v", out, err)
		}
		var typed *apperrors.Error
		if !errors.As(err, &typed) || typed.Reason != "aitable_composite_unknown" {
			t.Fatalf("delete missing records error = %#v", err)
		}
	})

	t.Run("remaining record is not success", func(t *testing.T) {
		caller := &upsertByKeyCaller{steps: []upsertByKeyStep{
			{text: `{"deletedCount":0}`},
			{text: `{"records":[{"recordId":"r1","cells":{"fld":"still here"}}]}`},
		}}
		out, err := runRecordDeleteCLI(t, caller, []string{"r1"})
		if err == nil || out != "" {
			t.Fatalf("delete remaining record = output:%q err:%v", out, err)
		}
	})
}

func TestCrossPlatformCoverageRecordDeletePartialCheckpointE2E(t *testing.T) {
	ids := recordIDFixtures(101)
	caller := &upsertByKeyCaller{steps: []upsertByKeyStep{
		{text: `{"deletedCount":100}`},
		{text: `{"records":[]}`},
		{text: `{"records":[]}`},
		{text: `{"records":[]}`},
		{text: `{"records":[]}`},
		{text: `{"records":[]}`},
		{text: `{"deletedCount":0}`},
		{text: `{"records":[{"recordId":"r100","cells":{}}]}`},
	}}
	out, err := runRecordDeleteCLI(t, caller, ids)
	if err == nil || out != "" {
		t.Fatalf("partial delete = output:%q err:%v", out, err)
	}
	var typed *apperrors.Error
	if !errors.As(err, &typed) || typed.Reason != "aitable_composite_partial_success" {
		t.Fatalf("partial delete error = %#v", err)
	}
	result := typed.Details["result"].(compositeResult)
	if result.Checkpoint["nextOffset"] != 100 || result.CompletedCount != 100 {
		t.Fatalf("partial delete result = %#v", result)
	}
}
