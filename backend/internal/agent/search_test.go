package agent

import "testing"

func TestActionSearchUnderstandsReadAndWriteVerbsWithoutWideningRights(t *testing.T) {
	l := &loop{e: &Engine{}, run: testRun()}
	for _, q := range []string{"list ledgers", "show ledgers", "get ledgers"} {
		hits := l.search(q)
		if len(hits) != 1 || hits[0].ActionID != "get.ledgers" {
			t.Fatalf("%q: %+v", q, hits)
		}
	}
	hits := l.search("create payment voucher")
	if len(hits) != 1 || hits[0].ActionID != "post.vouchers.payment" {
		t.Fatalf("delegated payment: %+v", hits)
	}
	if hits := l.search("create receipt voucher"); len(hits) != 0 {
		t.Fatalf("undelegated receipt exposed: %+v", hits)
	}
}
