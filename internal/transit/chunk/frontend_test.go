package chunk

import (
	"bytes"
	"encoding/json"
	"math"
	"os/exec"
	"testing"
)

// The browser and server must agree, including null versus a legitimate zero.
func TestFrontendParity(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed for frontend parity")
	}
	var rows []ChunkView
	for i := range 40 {
		rows = append(rows, ChunkView{RouteID: string(rune('A' + i%3)), Band: Bands[i%3], Trips: i + 1, Scheduled: i + 5, Cancelled: i % 4, NoNotice: i % 2, Quality: Quality{Version: Version, OTPCount: i + 10, OTPOnTime: i + 5, WaitObservedArea: float64(i * 1000), WaitScheduledArea: float64(i * 1200), WindowSeconds: float64(i * 60), CVWeightedSum: float64(i), CVWeight: float64(i * 2)}})
	}
	rows = append(rows, ChunkView{RouteID: "legacy", Trips: 500, Scheduled: 500, Cancelled: 500})
	payload, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	script := `const fs=require('fs'),vm=require('vm');global.window={};vm.runInThisContext(fs.readFileSync('../../../static/transit/chunks.js','utf8'));let rows=JSON.parse(fs.readFileSync(0,'utf8'));console.log(JSON.stringify(['','morning','midday','evening'].flatMap(b=>['otp','cancel','notice','wait','ewt','cv'].map(m=>window.transitChunks.kpi(rows,m,b)))));`
	cmd := exec.Command(node, "-e", script)
	cmd.Stdin = bytes.NewReader(payload)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node: %s: %v", output, err)
	}
	var values []*float64
	if err := json.Unmarshal(output, &values); err != nil {
		t.Fatal(err)
	}
	i := 0
	for _, b := range []string{"", "morning", "midday", "evening"} {
		for _, m := range []Metric{MetricOTP, MetricCancel, MetricNotice, MetricWait, MetricEWT, MetricCv} {
			got, ok := KPI(rows, m, b)
			if ok != (values[i] != nil) || (ok && math.Abs(got-*values[i]) > 1e-10) {
				t.Fatalf("%s/%s: Go=%v/%v JS=%v", m, b, got, ok, values[i])
			}
			i++
		}
	}
}
func TestFrontendCalendarWindows(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node needed")
	}
	script := `const fs=require('fs'),vm=require('vm'),assert=require('assert/strict');global.window={};vm.runInThisContext(fs.readFileSync('../../../static/transit/chunks.js','utf8'));const h=window.transitChunks;
    const rows=Array.from({length:60},(_,i)=>({date:new Date(Date.UTC(2026,0,i+1)).toISOString().slice(0,10),route_id:'R',metric_version:1,otp_count:10,otp_on_time:8}));
    let s=h.trendSeries(rows,'otp','2026-01-01','2026-03-01','');assert.equal(s[28].rolling,null);assert.equal(s[29].rolling,80);
    const missing=rows.filter(r=>r.date<'2026-01-10'||r.date>'2026-01-29');s=h.trendSeries(missing,'otp','2026-01-01','2026-03-01','');assert.equal(s[29].rolling,null);assert.equal(s[28].daily,null);
    s=h.trendSeries(rows,'otp','2026-01-01','2026-03-01','sunday');assert.equal(s.at(-1).rolling,80);assert.equal(s.at(-2).rolling,null);
    assert.equal(h.kpi([{metric_version:0,otp_count:10,otp_on_time:10}],'otp',''),null);
    assert.equal(h.kpi([{metric_version:1,cv_weight:20,cv_weighted_sum:0}],'cv',''),0);`
	if output, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("%s: %v", output, err)
	}
}
