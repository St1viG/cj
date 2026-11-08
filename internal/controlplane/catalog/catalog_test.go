package catalog_test

import (
	"context"
	"strings"
	"testing"

	"github.com/St1viG/cj/internal/controlplane/catalog"
	"github.com/St1viG/cj/internal/store/storetest"
)

func TestDemoCatalogSeedsIdempotently(t *testing.T) {
	ctx := context.Background()
	s := storetest.New(t)
	t.Setenv("KONDUKTER_IMAGE_BASE_URL", "http://images.test:8081")
	f, err := catalog.Parse(catalog.Demo)
	if err != nil {
		t.Fatal(err)
	}
	var reps []catalog.Report
	for range 2 {
		rep, err := catalog.Seed(ctx, s, f)
		if err != nil {
			t.Fatal(err)
		}
		reps = append(reps, rep)
	}
	if reps[0].Shapes != 2 || reps[0].Offers != 2 || reps[0].Images != 2 || len(reps[0].Skipped) != 1 ||
		!strings.Contains(reps[0].Skipped[0], "ubuntu-gpu-pascal") {
		t.Fatalf("report = %+v", reps[0])
	}
	offers, _ := s.ListOffers(ctx)
	images, _ := s.ListImages(ctx)
	shapes, _ := s.ListShapes(ctx)
	if len(offers) != 2 || len(images) != 2 || len(shapes) != 2 {
		t.Fatalf("after two seeds: %d offers, %d images, %d shapes", len(offers), len(images), len(shapes))
	}
	for _, o := range offers {
		if o.Offer.Currency != "EUR" || o.Offer.Region != "demo" || !o.Offer.Active {
			t.Fatalf("offer = %+v", o.Offer)
		}
	}
	archs := map[string]bool{}
	for _, img := range images {
		archs[img.Arch] = true
		if !strings.HasPrefix(img.Digest, "sha256:") || img.SizeBytes == 0 {
			t.Fatalf("image = %+v", img)
		}
	}
	if !archs["amd64"] || !archs["arm64"] {
		t.Fatalf("ubuntu-cpu archs = %v", archs)
	}
}

func TestParseRejectsBrokenCatalogs(t *testing.T) {
	for name, doc := range map[string]string{
		"unknown key":      "shapes: [{name: x, vcpu: 1, ram_mib: 1, disk_gib: 1, gpus: 1}]",
		"dangling offer":   "regions: [{name: r}]\noffers: [{shape: nope, region: r, price_per_hour_minor: 1, currency: EUR}]",
		"bad digest":       "images: [{name: i, version: v, arch: amd64, url: u, digest: md5:abc}]",
		"half accelerator": "shapes: [{name: g, vcpu: 1, ram_mib: 1, disk_gib: 1, accelerator: {kind: nvidia}}]",
	} {
		if _, err := catalog.Parse([]byte(doc)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
