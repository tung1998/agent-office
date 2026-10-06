package ops

import "testing"

func TestParsePS(t *testing.T) {
	lines := []byte(`{"ID":"abc","Name":"shop-redis-1","Service":"redis","State":"running","Status":"Up 2 minutes (healthy)","Health":"healthy","Publishers":[{"URL":"0.0.0.0","TargetPort":6379,"PublishedPort":6379,"Protocol":"tcp"},{"URL":"::","TargetPort":6379,"PublishedPort":6379,"Protocol":"tcp"}]}
{"ID":"def","Name":"shop-app-1","Service":"app","State":"exited","Status":"Exited (1) 3 seconds ago","Publishers":[]}`)
	rows := parsePS(lines)
	if len(rows) != 2 || rows[0].Service != "redis" || rows[1].State != "exited" {
		t.Fatalf("%+v", rows)
	}
	arr := parsePS([]byte(`[{"ID":"x","Service":"web","State":"running"}]`))
	if len(arr) != 1 || arr[0].Service != "web" {
		t.Fatalf("%+v", arr)
	}
	if len(parsePS(nil)) != 0 {
		t.Fatal("empty")
	}
}
