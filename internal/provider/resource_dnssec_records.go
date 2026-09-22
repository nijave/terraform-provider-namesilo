// SPDX-License-Identifier: GPL-3.0-or-later

package provider

import (
	"context"
	"fmt"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/nijave/terraform-provider-namesilo/internal/namesilo"
)

var (
	_ resource.Resource                = (*dnssecRecordsResource)(nil)
	_ resource.ResourceWithConfigure   = (*dnssecRecordsResource)(nil)
	_ resource.ResourceWithImportState = (*dnssecRecordsResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*dnssecRecordsResource)(nil)
)

// dsDigestPattern is the hexadecimal shape a DS digest must have. It accepts
// either case; the value is lowercased at plan time regardless.
var dsDigestPattern = regexp.MustCompile(`^[0-9a-fA-F]+$`)

// dnssecRecordsResource manages one domain's DS records. The API's identity
// for a record is a tuple with no add-only or remove-only middle ground, so
// the resource is authoritative: the domain's delegation is exactly the
// `records` set (§16).
type dnssecRecordsResource struct {
	client *namesilo.Client
}

// NewDNSSecRecordsResource returns the namesilo_dnssec_records resource.
func NewDNSSecRecordsResource() resource.Resource {
	return &dnssecRecordsResource{}
}

// dnssecRecordsResourceModel is namesilo_dnssec_records's state model.
type dnssecRecordsResourceModel struct {
	Domain  types.String `tfsdk:"domain"`
	Records types.Set    `tfsdk:"records"`
	ID      types.String `tfsdk:"id"`
}

// dsRecordModel is one element of the records set.
type dsRecordModel struct {
	KeyTag     types.Int64  `tfsdk:"key_tag"`
	Algorithm  types.Int64  `tfsdk:"algorithm"`
	DigestType types.Int64  `tfsdk:"digest_type"`
	Digest     types.String `tfsdk:"digest"`
}

func (r *dnssecRecordsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dnssec_records"
}

func (r *dnssecRecordsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages one domain's DS records — the delegation signer records that " +
			"bind the domain to its DNSSEC keys. The API has no partial update, so this resource is " +
			"authoritative: the domain's DNSSEC delegation is exactly the `records` set.\n\n" +
			"Destroying this resource deletes the DS records and disables DNSSEC for the domain. " +
			"Publishing a DS record that does not match the zone's keys makes the domain fail " +
			"validation, so during a key roll add the new record before removing the old one " +
			"(the provider reconciles in that order), and destroy this resource only when the zone " +
			"is being retired or DNSSEC is intentionally disabled. An empty `records` set declares " +
			"that the domain is unsigned; it is also what importing an unsigned domain produces.",
		Attributes: map[string]schema.Attribute{
			"domain": schema.StringAttribute{
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				MarkdownDescription: "The domain whose DS records to manage, e.g. `example.com`. " +
					"Changing it forces a replacement: the resource is pointed at a different domain " +
					"rather than renamed. Also the import ID.",
			},
			"records": schema.SetNestedAttribute{
				Required: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"key_tag": schema.Int64Attribute{
							Required: true,
							Validators: []validator.Int64{
								int64validator.Between(0, 65535),
							},
							MarkdownDescription: "The key tag: the RFC 4034 identifier of the zone's " +
								"DNSKEY that this DS record points at. Valid values are 0 through " +
								"65535. The range is wider than the currently assigned IANA values so " +
								"a configuration does not break the day IANA assigns a new one; see " +
								"https://www.iana.org/assignments/dns-sec-alg-numbers/",
						},
						"algorithm": schema.Int64Attribute{
							Required: true,
							Validators: []validator.Int64{
								int64validator.Between(0, 255),
							},
							MarkdownDescription: "The DNSSEC signing algorithm number of the zone's " +
								"DNSKEY, e.g. 8 (RSA/SHA-256) or 13 (ECDSA P-256/SHA-256). Valid " +
								"values are 0 through 255, deliberately wider than the currently " +
								"assigned IANA values so a new assignment does not break working " +
								"configurations; see " +
								"https://www.iana.org/assignments/dns-sec-alg-numbers/",
						},
						"digest_type": schema.Int64Attribute{
							Required: true,
							Validators: []validator.Int64{
								int64validator.Between(0, 255),
							},
							MarkdownDescription: "The digest algorithm that produced `digest`, e.g. " +
								"2 (SHA-256). Valid values are 0 through 255, deliberately wider " +
								"than the currently assigned IANA values; see " +
								"https://www.iana.org/assignments/ds-rr-types/",
						},
						"digest": schema.StringAttribute{
							Required: true,
							Validators: []validator.String{
								stringvalidator.RegexMatches(dsDigestPattern, "digest must be hexadecimal"),
							},
							MarkdownDescription: "The hexadecimal digest of the zone's DNSKEY, whose " +
								"length is fixed by `digest_type`. Case is insignificant: it is " +
								"stored lowercased and normalized at plan time, so a differently-cased " +
								"value does not diff.",
						},
					},
				},
				MarkdownDescription: "The complete set of DS records. A set, not a list: identical " +
					"tuples collapse instead of reaching the API as a duplicate-record error, and " +
					"order has no meaning. An empty set is the way to declare the domain unsigned. " +
					"When this resource is destroyed every record present is deleted, which disables " +
					"DNSSEC for the domain.",
			},
			"id": schema.StringAttribute{
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
				MarkdownDescription: "The domain name. It is inert (Terraform keys state by resource " +
					"address), it makes `tofu state show` readable, and it is the import ID.",
			},
		},
	}
}

// Configure receives the provider's *namesilo.Client. A wrong type is a
// provider bug, and the diagnostic names the type actually received.
func (r *dnssecRecordsResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*namesilo.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected resource Configure type",
			fmt.Sprintf("Expected *namesilo.Client, got %T. This is a provider bug; please report it.", req.ProviderData),
		)
		return
	}
	r.client = client
}

// Create and Update both reconcile, because the API's unit of change is the
// whole set. Create still lists first: the domain may carry DS records from
// before the resource existed, and adopting them silently is what keeps a
// pre-existing delegation from failing on a duplicate add.
func (r *dnssecRecordsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan dnssecRecordsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	r.reconcile(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Update reconciles the planned set, exactly as Create does. A change to
// domain plans a replacement, so Update never sees a domain change.
func (r *dnssecRecordsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan dnssecRecordsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	r.reconcile(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// reconcile makes the domain's DS records match the plan: list, diff, then
// add the missing records before deleting the extra ones. Adds run first so a
// key roll never leaves a window with neither the old nor the new DS
// published (§16). DiffDSRecords compares canonical tuples, so a record that
// differs only in digest case is not re-added, and a record that is already
// gone is not re-deleted (§4 invariant 3).
func (r *dnssecRecordsResource) reconcile(ctx context.Context, plan *dnssecRecordsResourceModel, diags *diag.Diagnostics) {
	domain := plan.Domain.ValueString()

	current, err := r.client.ListDSRecords(ctx, domain)
	if err != nil {
		addAPIError(diags, "namesilo_dnssec_records", err)
		return
	}
	desired, d := recordsFromDSSet(ctx, plan.Records)
	diags.Append(d...)
	if diags.HasError() {
		return
	}

	toAdd, toRemove := namesilo.DiffDSRecords(current, desired)
	for _, record := range toAdd {
		if err := r.client.AddDSRecord(ctx, domain, record); err != nil {
			addAPIError(diags, "namesilo_dnssec_records", err)
			return
		}
	}
	for _, record := range toRemove {
		if err := r.client.DeleteDSRecord(ctx, domain, record); err != nil {
			addAPIError(diags, "namesilo_dnssec_records", err)
			return
		}
	}

	normalized := make([]namesilo.DSRecord, 0, len(desired))
	for _, record := range desired {
		normalized = append(normalized, namesilo.NormalizeDSRecord(record))
	}
	set, d := dsSetFromRecords(ctx, normalized)
	diags.Append(d...)
	if diags.HasError() {
		return
	}
	plan.Records = set
	plan.ID = plan.Domain
}

// Read refreshes the records from dnsSecListRecords and normalizes each digest
// so it matches what ModifyPlan folds into the plan. An empty list is valid
// here (unlike nameservers): it means the domain is unsigned, and it is stored
// as an empty set (§4 invariant 2). Any API error leaves state untouched.
func (r *dnssecRecordsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state dnssecRecordsResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	records, err := r.client.ListDSRecords(ctx, state.Domain.ValueString())
	if err != nil {
		addAPIError(&resp.Diagnostics, "namesilo_dnssec_records", err)
		return
	}

	normalized := make([]namesilo.DSRecord, 0, len(records))
	for _, record := range records {
		normalized = append(normalized, namesilo.NormalizeDSRecord(record))
	}
	set, diags := dsSetFromRecords(ctx, normalized)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	state.Records = set
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Delete lists the domain's DS records and deletes each one present, which
// disables DNSSEC for the domain (§4 invariant 5). A record already removed
// outside Terraform is not deleted again, and an empty list makes Delete a
// no-op. On an API error the diagnostic is returned and state is kept, so the
// destroy can be retried.
func (r *dnssecRecordsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state dnssecRecordsResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	records, err := r.client.ListDSRecords(ctx, state.Domain.ValueString())
	if err != nil {
		addAPIError(&resp.Diagnostics, "namesilo_dnssec_records", err)
		return
	}
	for _, record := range records {
		if err := r.client.DeleteDSRecord(ctx, state.Domain.ValueString(), record); err != nil {
			addAPIError(&resp.Diagnostics, "namesilo_dnssec_records", err)
			return
		}
	}
}

// ImportState takes the domain as the import ID, seeds id from it, and lets
// Read fill records from the API (§10). An unsigned domain imports as
// records = [], a valid configuration.
func (r *dnssecRecordsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("domain"), req.ID)...)
}

// ModifyPlan normalizes the planned records set so it matches the normalized
// state Read writes; without it an uppercase digest in configuration would
// produce a perpetual diff (§6.8). Destroy plans carry no planned state, and
// unknown values are left alone: the apply resolves them.
func (r *dnssecRecordsResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan dnssecRecordsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.Records.IsNull() || !setWhollyKnown(plan.Records) {
		return
	}

	records, diags := recordsFromDSSet(ctx, plan.Records)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	normalized := make([]namesilo.DSRecord, 0, len(records))
	for _, record := range records {
		normalized = append(normalized, namesilo.NormalizeDSRecord(record))
	}
	set, diags := dsSetFromRecords(ctx, normalized)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("records"), set)...)
}
