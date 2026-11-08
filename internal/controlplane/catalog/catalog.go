// Package catalog loads and seeds shapes, offers, images and regions
// (SPEC §6.2, ADR-0007, ADR-0015).
package catalog

import (
	"bytes"
	"context"
	_ "embed"

	"fmt"
	"os"
	"regexp"

	"go.yaml.in/yaml/v3"

	"github.com/St1viG/cj/internal/store"
	"github.com/St1viG/cj/internal/store/db"
)

// Demo is the built-in demo catalog (ROADMAP §1.3).
//
//go:embed demo.yaml
var Demo []byte

// File is a catalog document.
type File struct {
	Regions []Region `yaml:"regions"`
	Shapes  []Shape  `yaml:"shapes"`
	Offers  []Offer  `yaml:"offers"`
	Images  []Image  `yaml:"images"`
}

// Region is a placement region.
type Region struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// Shape is a size, with optional accelerators.
type Shape struct {
	Name        string       `yaml:"name"`
	VCPU        int32        `yaml:"vcpu"`
	RAMMiB      int64        `yaml:"ram_mib"`
	DiskGiB     int64        `yaml:"disk_gib"`
	Accelerator *Accelerator `yaml:"accelerator"`
}

// Accelerator is a shape's accelerator request.
type Accelerator struct {
	Kind  string `yaml:"kind"`
	Model string `yaml:"model"`
	Count int32  `yaml:"count"`
}

// Offer puts a shape on sale.
type Offer struct {
	Shape             string `yaml:"shape"`
	Region            string `yaml:"region"`
	CapacityClass     string `yaml:"capacity_class"`
	PricePerHourMinor int64  `yaml:"price_per_hour_minor"`
	Currency          string `yaml:"currency"`
	Inactive          bool   `yaml:"inactive"`
}

// Image is a curated guest image.
type Image struct {
	Name             string   `yaml:"name"`
	Version          string   `yaml:"version"`
	Arch             string   `yaml:"arch"`
	OS               string   `yaml:"os"`
	AccelStack       string   `yaml:"accel_stack"`
	CompatibleModels []string `yaml:"compatible_models"`
	URL              string   `yaml:"url"`
	Digest           string   `yaml:"digest"`
	SizeBytes        int64    `yaml:"size_bytes"`
	Format           string   `yaml:"format"`
	Deprecated       bool     `yaml:"deprecated"`
}

var digestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Parse decodes a catalog, expanding ${VAR} from the environment, and
// checks references between its sections.
func Parse(raw []byte) (File, error) {
	var f File
	dec := yaml.NewDecoder(bytes.NewReader([]byte(os.ExpandEnv(string(raw)))))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return f, fmt.Errorf("catalog: %w", err)
	}
	regions, shapes := map[string]bool{}, map[string]bool{}
	for _, r := range f.Regions {
		regions[r.Name] = true
	}
	for _, s := range f.Shapes {
		if s.Name == "" || s.VCPU <= 0 || s.RAMMiB <= 0 || s.DiskGiB <= 0 {
			return f, fmt.Errorf("catalog: shape %q needs a name and positive vcpu, ram_mib, disk_gib", s.Name)
		}
		if a := s.Accelerator; a != nil && (a.Kind == "" || a.Model == "" || a.Count <= 0) {
			return f, fmt.Errorf("catalog: shape %q: accelerator needs kind, model and count", s.Name)
		}
		shapes[s.Name] = true
	}
	for _, o := range f.Offers {
		if !shapes[o.Shape] || !regions[o.Region] {
			return f, fmt.Errorf("catalog: offer %s/%s references an unknown shape or region", o.Shape, o.Region)
		}
	}
	for _, img := range f.Images {
		if img.Digest != "" && !digestRE.MatchString(img.Digest) {
			return f, fmt.Errorf("catalog: image %s %s %s: digest must be sha256:<64 hex>", img.Name, img.Version, img.Arch)
		}
	}
	return f, nil
}

// Report says what Seed did.
type Report struct {
	Regions, Shapes, Offers, Images int
	// Skipped lists images left out because they have no digest yet.
	Skipped []string
}

// Seed upserts the catalog in one transaction. It never deletes; running it
// twice is a no-op.
func Seed(ctx context.Context, s *store.Store, f File) (Report, error) {
	var rep Report
	err := s.WithTx(ctx, func(tx store.Tx) error {
		rep = Report{}
		for _, r := range f.Regions {
			if err := tx.UpsertRegion(ctx, db.UpsertRegionParams{Name: r.Name, Description: r.Description}); err != nil {
				return err
			}
			rep.Regions++
		}
		shapeIDs := map[string]db.Shape{}
		for _, sh := range f.Shapes {
			p := db.UpsertShapeParams{Name: sh.Name, Vcpu: sh.VCPU, RamMib: sh.RAMMiB, DiskGib: sh.DiskGiB}
			if a := sh.Accelerator; a != nil {
				p.AccelKind, p.AccelModel, p.AccelCount = a.Kind, a.Model, a.Count
			}
			row, err := tx.UpsertShape(ctx, p)
			if err != nil {
				return fmt.Errorf("shape %s: %w", sh.Name, err)
			}
			shapeIDs[sh.Name] = row
			rep.Shapes++
		}
		for _, o := range f.Offers {
			class := o.CapacityClass
			if class == "" {
				class = "dedicated"
			}
			if _, err := tx.UpsertOffer(ctx, db.UpsertOfferParams{
				ShapeID: shapeIDs[o.Shape].ID, Region: o.Region, CapacityClass: class,
				PricePerHourMinor: o.PricePerHourMinor, Currency: o.Currency, Active: !o.Inactive,
			}); err != nil {
				return fmt.Errorf("offer %s/%s: %w", o.Shape, o.Region, err)
			}
			rep.Offers++
		}
		for _, img := range f.Images {
			label := img.Name + " " + img.Version + " " + img.Arch
			if img.Digest == "" {
				rep.Skipped = append(rep.Skipped, label)
				continue
			}
			status := "active"
			if img.Deprecated {
				status = "deprecated"
			}
			format := img.Format
			if format == "" {
				format = "qcow2"
			}
			models := img.CompatibleModels
			if models == nil {
				models = []string{}
			}
			if _, err := tx.UpsertImage(ctx, db.UpsertImageParams{
				Name: img.Name, Version: img.Version, Arch: img.Arch, Os: img.OS, AccelStack: img.AccelStack,
				CompatibleModels: models, Digest: img.Digest, Url: img.URL, SizeBytes: img.SizeBytes,
				Format: format, Status: status,
			}); err != nil {
				return fmt.Errorf("image %s: %w", label, err)
			}
			rep.Images++
		}
		return nil
	})
	if err != nil {
		return Report{}, err
	}
	return rep, nil
}
