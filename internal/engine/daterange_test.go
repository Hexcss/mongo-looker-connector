package engine

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestDateRangeBuildsIndexableUTCMatch(t *testing.T) {
	dates := DateRange{Field: "order.createdAt", StartDate: "2024-02-29", EndDate: "2024-03-01"}
	match, err := dates.match()
	start := time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 3, 2, 0, 0, 0, 0, time.UTC)
	want := bson.D{{Key: "$match", Value: bson.D{{Key: "order.createdAt", Value: bson.D{
		{Key: "$gte", Value: start}, {Key: "$lt", Value: end},
	}}}}}
	if err != nil || !reflect.DeepEqual(match, want) {
		t.Fatalf("match = %v, error = %v", match, err)
	}
	for _, bad := range []DateRange{
		{"date", "2024-02-30", "2024-03-15"}, {"date", "2024-03-15", "2024-03-14"},
		{"$or", "2024-03-15", "2024-03-15"}, {"path.$key", "2024-03-15", "2024-03-15"},
		{"escaped\\.name", "2024-03-15", "2024-03-15"}, {"a..b", "2024-03-15", "2024-03-15"},
	} {
		if _, err := bad.match(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted invalid range: %v", bad)
		}
	}
}
