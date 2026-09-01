package main

// ---------------------------------------------------------------------------
// Parcel — قرارداد پلاک ثبتی و مالکیت
//
// یک نکته حریم خصوصی که کل طراحی این قرارداد را شکل داده: روی کانالی که
// هشت سازمان عضو آن‌اند، کد ملی و نشانی مالک نباید در دفتر عمومی بنشیند.
// اداره برق لازم نیست بداند مالک پلاک همسایه کیست.
//
// راه‌حل فابریک برای این کار Private Data Collection است: اصل داده فقط روی
// peer سازمان‌های مجاز می‌نشیند و آنچه به همه بلاک‌ها می‌رود فقط هش آن است.
// پس هر سازمانی می‌تواند «اثبات کند» سندی که دارد همان سند اصلی است، بی‌آنکه
// محتوایش را ببیند.
// ---------------------------------------------------------------------------

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/hyperledger/fabric-contract-api-go/contractapi"
)

type ParcelContract struct {
	NetworkBase
}

// OwnerPII — داده هویتی مالک. فقط در PDC ownerPII می‌نشیند.
type OwnerPII struct {
	OwnerID     string `json:"ownerId"`
	FullName    string `json:"fullName"`
	NationalID  string `json:"nationalId"`
	Phone       string `json:"phone"`
	Address     string `json:"address"`
	DeedNumber  string `json:"deedNumber"`
	RecordedAt  int64  `json:"recordedAt"`
}

// RegisterParcel ثبت پلاک. داده هویتی از transient می‌آید نه از آرگومان —
// آرگومان‌های تراکنش در بلاک ذخیره می‌شوند و پاک‌شدنی نیستند.
//
// نحوه ارسال:
//   peer chaincode invoke ... -c '{"function":"RegisterParcel","Args":[...]}' \
//     --transient '{"ownerPII":"<base64 of JSON>"}'
func (c *ParcelContract) RegisterParcel(ctx contractapi.TransactionContextInterface,
	id, region, zone, ownerID string,
	landArea, existingArea, frontageCm, streetWidthCm int64,
	currentUse string, setbackArea int64, wornTexture, isGarden bool, treeCount int64) error {

	if err := requireRole(ctx, RoleDistrict, RoleRegulator); err != nil {
		return err
	}
	if landArea <= 0 {
		return fmt.Errorf("مساحت عرصه باید مثبت باشد")
	}
	if _, ok := UseTitleFa[currentUse]; !ok {
		return fmt.Errorf("کاربری فعلی ناشناخته: «%s»", currentUse)
	}
	exists, err := ctx.GetStub().GetState(mkKey(KeyParcel, id))
	if err != nil {
		return err
	}
	if exists != nil {
		return fmt.Errorf("پلاک «%s» قبلاً ثبت شده است", id)
	}

	now := txTime(ctx)
	p := Parcel{
		DocType: "parcel", ID: id, Region: region, Zone: zone, OwnerID: ownerID,
		LandArea: landArea, ExistingArea: existingArea,
		FrontageCm: frontageCm, StreetWidthCm: streetWidthCm,
		CurrentUse: currentUse, SetbackArea: setbackArea,
		WornTexture: wornTexture, IsGarden: isGarden, TreeCount: treeCount,
		CreatedAt: now, UpdatedAt: now,
	}

	// داده هویتی اگر آمده باشد در PDC می‌نشیند و هشش روی زنجیره.
	tm, err := ctx.GetStub().GetTransient()
	if err != nil {
		return fmt.Errorf("خطا در خواندن داده transient: %v", err)
	}
	if raw, ok := tm["ownerPII"]; ok && len(raw) > 0 {
		var pii OwnerPII
		if err := json.Unmarshal(raw, &pii); err != nil {
			return fmt.Errorf("داده هویتی مالک نامعتبر است: %v", err)
		}
		pii.OwnerID = ownerID
		pii.RecordedAt = now
		enc, err := json.Marshal(&pii)
		if err != nil {
			return err
		}
		if err := ctx.GetStub().PutPrivateData(PDCOwner, mkKey("PII", id), enc); err != nil {
			return fmt.Errorf("خطا در ثبت داده خصوصی مالک: %v", err)
		}
		p.OwnerPIIHash = hashPII(string(enc))
	}

	if err := putJSON(ctx, mkKey(KeyParcel, id), &p); err != nil {
		return err
	}
	return emit(ctx, "ParcelRegistered", id,
		fmt.Sprintf("پلاک %s در منطقه %s، پهنه %s، عرصه %d م²", id, region, zone, landArea),
		landArea)
}

func (c *ParcelContract) GetParcel(ctx contractapi.TransactionContextInterface,
	id string) (*Parcel, error) {
	var p Parcel
	if err := mustGetJSON(ctx, mkKey(KeyParcel, id), &p, "پلاک"); err != nil {
		return nil, err
	}
	return &p, nil
}

// GetOwnerPII خواندن داده هویتی. فقط از peer سازمان‌های عضو مجموعه خصوصی
// نتیجه می‌دهد؛ بقیه خطای دسترسی می‌گیرند — و همین درست است.
func (c *ParcelContract) GetOwnerPII(ctx contractapi.TransactionContextInterface,
	parcelID string) (*OwnerPII, error) {
	if err := requireRole(ctx, RoleRegulator, RoleDistrict, RoleFinance, RoleAuditor); err != nil {
		return nil, err
	}
	b, err := ctx.GetStub().GetPrivateData(PDCOwner, mkKey("PII", parcelID))
	if err != nil {
		return nil, fmt.Errorf("خطا در خواندن داده خصوصی: %v", err)
	}
	if b == nil {
		return nil, fmt.Errorf("داده هویتی مالک برای پلاک «%s» روی این peer موجود نیست", parcelID)
	}
	var pii OwnerPII
	if err := json.Unmarshal(b, &pii); err != nil {
		return nil, err
	}
	return &pii, nil
}

// TransferOwnership نقل و انتقال مالکیت.
func (c *ParcelContract) TransferOwnership(ctx contractapi.TransactionContextInterface,
	parcelID, newOwnerID, note string) error {

	if err := requireRole(ctx, RoleDistrict, RoleRegulator); err != nil {
		return err
	}
	var p Parcel
	if err := mustGetJSON(ctx, mkKey(KeyParcel, parcelID), &p, "پلاک"); err != nil {
		return err
	}
	if p.Blocked {
		return fmt.Errorf("پلاک «%s» در وضعیت بازداشت است و قابل انتقال نیست: %s", parcelID, p.BlockNote)
	}
	old := p.OwnerID
	p.OwnerID = newOwnerID
	p.UpdatedAt = txTime(ctx)

	// داده هویتی مالک جدید هم از transient می‌آید.
	tm, _ := ctx.GetStub().GetTransient()
	if raw, ok := tm["ownerPII"]; ok && len(raw) > 0 {
		var pii OwnerPII
		if err := json.Unmarshal(raw, &pii); err != nil {
			return fmt.Errorf("داده هویتی مالک جدید نامعتبر است: %v", err)
		}
		pii.OwnerID = newOwnerID
		pii.RecordedAt = p.UpdatedAt
		enc, _ := json.Marshal(&pii)
		if err := ctx.GetStub().PutPrivateData(PDCOwner, mkKey("PII", parcelID), enc); err != nil {
			return err
		}
		p.OwnerPIIHash = hashPII(string(enc))
	}
	if err := putJSON(ctx, mkKey(KeyParcel, parcelID), &p); err != nil {
		return err
	}
	return emit(ctx, "OwnershipTransferred", parcelID,
		fmt.Sprintf("انتقال مالکیت پلاک %s از %s به %s — %s", parcelID, old, newOwnerID, note), 0)
}

// SetBlock بازداشت یا رفع بازداشت پلاک (معارض ثبتی، رأی قضایی، بدهی معوق).
func (c *ParcelContract) SetBlock(ctx contractapi.TransactionContextInterface,
	parcelID string, blocked bool, note string) error {

	if err := requireRole(ctx, RoleRegulator, RoleAuditor, RoleUtility); err != nil {
		return err
	}
	var p Parcel
	if err := mustGetJSON(ctx, mkKey(KeyParcel, parcelID), &p, "پلاک"); err != nil {
		return err
	}
	p.Blocked = blocked
	p.BlockNote = note
	p.UpdatedAt = txTime(ctx)
	if err := putJSON(ctx, mkKey(KeyParcel, parcelID), &p); err != nil {
		return err
	}
	kind := "ParcelUnblocked"
	if blocked {
		kind = "ParcelBlocked"
	}
	return emit(ctx, kind, parcelID, note, 0)
}

// UpdateSurvey ثبت نتیجه بازدید نقشه‌برداری. کارشناس منطقه ابعاد واقعی را
// اندازه می‌گیرد و اگر با سند فرق دارد، همان مبنای محاسبه می‌شود.
func (c *ParcelContract) UpdateSurvey(ctx contractapi.TransactionContextInterface,
	parcelID string, landArea, existingArea, frontageCm, streetWidthCm, setbackArea int64,
	note string) error {

	if err := requireRole(ctx, RoleDistrict); err != nil {
		return err
	}
	var p Parcel
	if err := mustGetJSON(ctx, mkKey(KeyParcel, parcelID), &p, "پلاک"); err != nil {
		return err
	}
	oldArea := p.LandArea
	if landArea > 0 {
		p.LandArea = landArea
	}
	if existingArea >= 0 {
		p.ExistingArea = existingArea
	}
	if frontageCm > 0 {
		p.FrontageCm = frontageCm
	}
	if streetWidthCm > 0 {
		p.StreetWidthCm = streetWidthCm
	}
	if setbackArea >= 0 {
		p.SetbackArea = setbackArea
	}
	p.UpdatedAt = txTime(ctx)
	if err := putJSON(ctx, mkKey(KeyParcel, parcelID), &p); err != nil {
		return err
	}
	return emit(ctx, "ParcelSurveyed", parcelID,
		fmt.Sprintf("بازدید نقشه‌برداری: عرصه از %d به %d م² — %s", oldArea, p.LandArea, note),
		p.LandArea)
}

// ---------------------------- حق توسعه باغات ----------------------------

// DevelopmentRight — حق توسعه قابل واگذاری یک پلاک فرستنده.
type DevelopmentRight struct {
	DocType   string `json:"docType"`
	ParcelID  string `json:"parcelId"`
	OwnerID   string `json:"ownerId"`
	Region    string `json:"region"`
	Area      int64  `json:"area"`      // متراژ حق توسعه (متر مربع)
	Certified bool   `json:"certified"` // تأیید نهایی شهرداری
	Redeemed  int64  `json:"redeemed"`  // مقدار واگذارشده
	Basis     string `json:"basis"`
	CreatedAt int64  `json:"createdAt"`
}

// CertifyDevelopmentRight صدور گواهی حق توسعه برای پلاک باغی یا بافت تاریخی.
//
// این هسته سیاست حفظ باغات شیراز است. منطق ساده و مهم است: مالک باغی که
// اجازه ساخت ندارد، امروز عملاً انگیزه دارد درخت را از بین ببرد تا پلاک
// «بایر» شود. اگر همان مالک بتواند حق توسعه‌ای را که هرگز نمی‌تواند در محل
// اعمال کند بفروشد، حفظ باغ برایش سودآور می‌شود. تراکم به جای دیگری منتقل
// می‌شود که زیرساختش را دارد، شهرداری از معامله کارمزد می‌گیرد، و باغ سرجایش
// می‌ماند. هیچ‌کدام از سه طرف بازنده نیستند.
func (c *ParcelContract) CertifyDevelopmentRight(ctx contractapi.TransactionContextInterface,
	parcelID string, area int64, basis string) (*DevelopmentRight, error) {

	if err := requireRole(ctx, RoleRegulator); err != nil {
		return nil, err
	}
	var p Parcel
	if err := mustGetJSON(ctx, mkKey(KeyParcel, parcelID), &p, "پلاک"); err != nil {
		return nil, err
	}
	if !p.IsGarden && !p.WornTexture {
		return nil, fmt.Errorf("حق توسعه فقط برای پلاک باغی یا واقع در بافت واجد حفاظت صادر می‌شود")
	}
	if p.Blocked {
		return nil, fmt.Errorf("پلاک در بازداشت است")
	}
	if area <= 0 {
		return nil, fmt.Errorf("متراژ حق توسعه باید مثبت باشد")
	}

	dr := DevelopmentRight{
		DocType: "devright", ParcelID: parcelID, OwnerID: p.OwnerID,
		Region: p.Region, Area: area, Certified: true,
		Basis: basis, CreatedAt: txTime(ctx),
	}
	if err := putJSON(ctx, mkKey("DEVRIGHT", parcelID), &dr); err != nil {
		return nil, err
	}
	if err := emit(ctx, "DevelopmentRightCertified", parcelID,
		fmt.Sprintf("حق توسعه %d م² برای پلاک %s (مالک %s) — %s", area, parcelID, p.OwnerID, basis),
		area); err != nil {
		return nil, err
	}
	return &dr, nil
}

func (c *ParcelContract) GetDevelopmentRight(ctx contractapi.TransactionContextInterface,
	parcelID string) (*DevelopmentRight, error) {
	var dr DevelopmentRight
	if err := mustGetJSON(ctx, mkKey("DEVRIGHT", parcelID), &dr, "حق توسعه"); err != nil {
		return nil, err
	}
	return &dr, nil
}

// RedeemDevelopmentRight ثبت واگذاری بخشی از حق توسعه. قرارداد SqmToken
// پس از این فراخوانی، معادل توکن را منتشر می‌کند.
func (c *ParcelContract) RedeemDevelopmentRight(ctx contractapi.TransactionContextInterface,
	parcelID string, amount int64) error {

	if err := requireRole(ctx, RoleRegulator); err != nil {
		return err
	}
	var dr DevelopmentRight
	if err := mustGetJSON(ctx, mkKey("DEVRIGHT", parcelID), &dr, "حق توسعه"); err != nil {
		return err
	}
	if !dr.Certified {
		return fmt.Errorf("حق توسعه این پلاک تأیید نشده است")
	}
	if dr.Redeemed+amount > dr.Area {
		return fmt.Errorf("مقدار درخواستی از باقی‌مانده حق توسعه بیشتر است (کل %d، واگذارشده %d)",
			dr.Area, dr.Redeemed)
	}
	dr.Redeemed += amount
	if err := putJSON(ctx, mkKey("DEVRIGHT", parcelID), &dr); err != nil {
		return err
	}
	return emit(ctx, "DevelopmentRightRedeemed", parcelID,
		fmt.Sprintf("واگذاری %d م² از حق توسعه پلاک %s", amount, parcelID), amount)
}

func (c *ParcelContract) ListParcelsByRegion(ctx contractapi.TransactionContextInterface,
	region string) ([]Parcel, error) {
	raw, err := queryByPrefix(ctx, KeyParcel)
	if err != nil {
		return nil, err
	}
	out := []Parcel{}
	for _, b := range raw {
		var p Parcel
		if err := json.Unmarshal(b, &p); err == nil && (region == "" || p.Region == region) {
			out = append(out, p)
		}
	}
	return out, nil
}

func main() {
	cc, err := contractapi.NewChaincode(&ParcelContract{})
	if err != nil {
		log.Panicf("خطا در ساخت قرارداد Parcel: %v", err)
	}
	if err := cc.Start(); err != nil {
		log.Panicf("خطا در اجرای قرارداد Parcel: %v", err)
	}
}
