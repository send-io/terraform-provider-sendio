package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/send-io/terraform-provider-sendio/internal/client"
)

var (
	_ resource.Resource                = &domainVerificationResource{}
	_ resource.ResourceWithImportState = &domainVerificationResource{}
)

// verifyPollInterval is how often a waiting verification re-checks DNS.
// Tests shorten it.
var verifyPollInterval = 15 * time.Second

func NewDomainVerificationResource() resource.Resource { return &domainVerificationResource{} }

type domainVerificationResource struct{ resourceBase }

type domainVerificationModel struct {
	ID                  types.String   `tfsdk:"id"`
	DomainID            types.String   `tfsdk:"domain_id"`
	WaitForVerification types.Bool     `tfsdk:"wait_for_verification"`
	Triggers            types.Map      `tfsdk:"triggers"`
	Status              types.String   `tfsdk:"status"`
	Verified            types.Bool     `tfsdk:"verified"`
	Timeouts            timeouts.Value `tfsdk:"timeouts"`
}

func (r *domainVerificationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_domain_verification"
}

func (r *domainVerificationResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Checks a domain's DNS records now (`POST /domains/{id}/verify`) and, with " +
			"`wait_for_verification`, waits until the domain is verified. Publish the records from " +
			"`sendio_domain.records` first (for example with your DNS provider's Terraform resources). " +
			"Destroying this resource changes nothing in send.io.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true, MarkdownDescription: "Same as `domain_id`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"domain_id": schema.StringAttribute{
				Required: true, MarkdownDescription: "Id of the domain to verify.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:    []validator.String{stringvalidator.RegexMatches(uuidRE, "must be a domain id (UUID)")},
			},
			"wait_for_verification": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				MarkdownDescription: "Wait until the domain status is `verified`, re-checking DNS every 15 seconds, " +
					"up to the create timeout (default 30 minutes).",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.RequiresReplace()},
			},
			"triggers": schema.MapAttribute{
				Optional: true, ElementType: types.StringType,
				MarkdownDescription: "Arbitrary values that re-run the verification when they change (for example DNS record ids).",
				PlanModifiers:       []planmodifier.Map{mapplanmodifier.RequiresReplace()},
			},
			"status": schema.StringAttribute{Computed: true, MarkdownDescription: "Domain status after the check (refreshed on every read)."},
			"verified": schema.BoolAttribute{
				Computed: true, MarkdownDescription: "Whether the domain status is `verified`.",
			},
			"timeouts": timeouts.Attributes(ctx, timeouts.Opts{Create: true}),
		},
	}
}

func (r *domainVerificationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan domainVerificationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := plan.Timeouts.Create(ctx, 30*time.Minute)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	id := plan.DomainID.ValueString()
	var d client.Domain
	reportTimeout := func() bool {
		if ctx.Err() != context.DeadlineExceeded {
			return false
		}
		resp.Diagnostics.AddError("Timed out waiting for domain verification",
			fmt.Sprintf("Domain %s (%s) is still %q after %s. Publish its DNS records and apply again, or raise timeouts.create.",
				d.Name, id, d.Status, timeout))
		return true
	}
	for {
		if err := r.client.Post(ctx, client.Path("/domains", id, "verify"), nil, nil); err != nil {
			if !reportTimeout() {
				apiErr(&resp.Diagnostics, "verify domain "+id, err)
			}
			return
		}
		if err := r.client.Get(ctx, client.Path("/domains", id), &d); err != nil {
			if !reportTimeout() {
				apiErr(&resp.Diagnostics, "read domain "+id, err)
			}
			return
		}
		if d.Status == "verified" || !plan.WaitForVerification.ValueBool() {
			break
		}
		if d.Status == "failed" {
			resp.Diagnostics.AddError("Domain verification failed",
				fmt.Sprintf("Domain %s (%s) is in status failed. Check its records and create the verification again.", d.Name, id))
			return
		}
		t := time.NewTimer(verifyPollInterval)
		select {
		case <-ctx.Done():
			t.Stop()
			if !reportTimeout() {
				apiErr(&resp.Diagnostics, "wait for domain verification", ctx.Err())
			}
			return
		case <-t.C:
		}
	}
	plan.ID = types.StringValue(id)
	plan.Status = types.StringValue(d.Status)
	plan.Verified = types.BoolValue(d.Status == "verified")
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *domainVerificationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state domainVerificationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.DomainID.ValueString()
	if id == "" {
		id = state.ID.ValueString()
	}
	var d client.Domain
	if err := r.client.Get(ctx, client.Path("/domains", id), &d); err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		apiErr(&resp.Diagnostics, "read domain "+id, err)
		return
	}
	state.ID = types.StringValue(id)
	state.DomainID = types.StringValue(id)
	if state.WaitForVerification.IsNull() {
		state.WaitForVerification = types.BoolValue(false)
	}
	// A verification that waited promised a verified domain. When the domain
	// is no longer verified (records removed, say), drop it from state so the
	// next plan shows it being created again, and apply waits once more.
	if state.WaitForVerification.ValueBool() && d.Status != "verified" {
		resp.State.RemoveResource(ctx)
		return
	}
	state.Status = types.StringValue(d.Status)
	state.Verified = types.BoolValue(d.Status == "verified")
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update only happens for timeouts changes; every other attribute replaces.
func (r *domainVerificationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state domainVerificationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ID, plan.Status, plan.Verified = state.ID, state.Status, state.Verified
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *domainVerificationResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
	// Nothing to undo: a verification check has no server-side object.
}

func (r *domainVerificationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("domain_id"), req.ID)...)
}
