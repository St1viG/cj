package api

import (
	"context"
	"slices"

	"connectrpc.com/connect"

	apiv1 "github.com/St1viG/cj/gen/go/kondukter/api/v1"
	"github.com/St1viG/cj/internal/store/db"
)

type catalogHandler struct{ *API }

// Compatible reports whether img can run on shape: an accelerator shape
// needs an image whose stack supports its model; a CPU shape needs an
// image without an accelerator stack. Deprecated images are never offered.
func Compatible(shape db.Shape, img db.Image) bool {
	if img.Status != "active" {
		return false
	}
	if shape.AccelCount > 0 {
		return img.AccelStack != "" && (len(img.CompatibleModels) == 0 || slices.Contains(img.CompatibleModels, shape.AccelModel))
	}
	return img.AccelStack == ""
}

// archs lists the architectures with a compatible image for shape.
func archs(shape db.Shape, images []db.Image) []string {
	var out []string
	for _, img := range images {
		if Compatible(shape, img) && !slices.Contains(out, img.Arch) {
			out = append(out, img.Arch)
		}
	}
	slices.Sort(out)
	return out
}

func offerProto(o db.Offer, sh db.Shape, images []db.Image) *apiv1.Offer {
	return &apiv1.Offer{
		Id: o.ID.String(), Shape: shapeProto(sh), Region: o.Region,
		CapacityClass: capacityClassProto(o.CapacityClass),
		PricePerHour:  moneyProto(o.PricePerHourMinor, o.Currency),
		Active:        o.Active, Archs: archs(sh, images),
	}
}

func (h catalogHandler) ListOffers(ctx context.Context, req *connect.Request[apiv1.ListOffersRequest]) (*connect.Response[apiv1.ListOffersResponse], error) {
	f := req.Msg
	offers, err := h.Store.ListOffers(ctx)
	if err != nil {
		return nil, h.internal(ctx, err)
	}
	images, err := h.Store.ListImages(ctx)
	if err != nil {
		return nil, h.internal(ctx, err)
	}
	out := &apiv1.ListOffersResponse{}
	for _, o := range offers {
		sh := o.Shape
		as := archs(sh, images)
		switch {
		case !o.Offer.Active && !f.GetIncludeInactive():
		case f.GetRegion() != "" && o.Offer.Region != f.GetRegion():
		case f.GetAcceleratorKind() == "none" && sh.AccelCount > 0:
		case f.GetAcceleratorKind() != "" && f.GetAcceleratorKind() != "none" && sh.AccelKind != f.GetAcceleratorKind():
		case f.GetAcceleratorModel() != "" && sh.AccelModel != f.GetAcceleratorModel():
		case f.GetArch() != "" && !slices.Contains(as, f.GetArch()):
		case f.GetCapacityClass() != apiv1.CapacityClass_CAPACITY_CLASS_UNSPECIFIED && o.Offer.CapacityClass != capacityClassName(f.GetCapacityClass()):
		case int64(f.GetMinVcpu()) > int64(sh.Vcpu):
		case f.GetMinRamMib() > uint64(max(sh.RamMib, 0)):
		default:
			out.Offers = append(out.Offers, offerProto(o.Offer, sh, images))
		}
	}
	return connect.NewResponse(out), nil
}

func (h catalogHandler) ListImages(ctx context.Context, req *connect.Request[apiv1.ListImagesRequest]) (*connect.Response[apiv1.ListImagesResponse], error) {
	images, err := h.Store.ListImages(ctx)
	if err != nil {
		return nil, h.internal(ctx, err)
	}
	var shape *db.Shape
	if req.Msg.GetOfferId() != "" {
		id, err := parseID(req.Msg.GetOfferId(), "offer")
		if err != nil {
			return nil, err
		}
		o, err := h.Store.GetOffer(ctx, id)
		if err != nil {
			return nil, notFound("offer")
		}
		shape = &o.Shape
	}
	out := &apiv1.ListImagesResponse{}
	for _, img := range images {
		switch {
		case req.Msg.GetArch() != "" && img.Arch != req.Msg.GetArch():
		case shape != nil && !Compatible(*shape, img):
		default:
			out.Images = append(out.Images, imageProto(img))
		}
	}
	return connect.NewResponse(out), nil
}

func (h catalogHandler) ListRegions(ctx context.Context, _ *connect.Request[apiv1.ListRegionsRequest]) (*connect.Response[apiv1.ListRegionsResponse], error) {
	regions, err := h.Store.ListRegions(ctx)
	if err != nil {
		return nil, h.internal(ctx, err)
	}
	out := &apiv1.ListRegionsResponse{}
	for _, r := range regions {
		out.Regions = append(out.Regions, &apiv1.Region{Name: r.Name, Description: r.Description})
	}
	return connect.NewResponse(out), nil
}
