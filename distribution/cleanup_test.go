package distribution

import (
	"context"
	"log/slog"
	"testing"

	physicalio "github.com/tetsup/flexraid/io"
	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

func TestCleanupSkipsExcludedObjects(t *testing.T) {
	id, err := shared.ParseObjectID("12345678-9abc-4def-8abc-1234567890ab")
	if err != nil {
		t.Fatal(err)
	}
	layer := &Distribution{
		logger:   slog.Default(),
		ios:      []*physicalio.IO{nil},
		objects:  make(map[shared.ObjectID]map[storage.StripeID]*stripeInfo),
		versions: make(map[stripeIDKey]map[storage.StripeVersion]*versionInfo),
		orphans: map[shared.ObjectID][]chunkLocation{
			id: {{ioIndex: 0}},
		},
	}
	_, err = layer.Cleanup(CleanupRequest{
		Context: context.Background(), InvalidStripes: true, ExcludeObjectIDs: []shared.ObjectID{id},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(layer.orphans[id]) != 1 {
		t.Fatalf("excluded object's orphan records were changed: %d", len(layer.orphans[id]))
	}
}
