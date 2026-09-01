package main

// ---------------------------------------------------------------------------
// shared.go — پایه مشترک همه قراردادهای شبکه پروانه ساختمانی شیراز
//
// این فایل توسط scripts/sync-shared.sh در پوشه هر قرارداد کپی می‌شود، چون
// هر قرارداد یک ماژول Go مستقل است (external builder از نوع prebuilt هر
// دایرکتوری را جدا کامپایل می‌کند).
//
// ── سه قاعده قطعیت که کل این فایل را شکل می‌دهند ─────────────────────────
//
// ۱) هیچ time.Now() — زمان فقط از GetTxTimestamp. هر peer در لحظه متفاوتی
//    اجرا می‌کند؛ time.Now() یعنی read-set متفاوت یعنی ENDORSEMENT_POLICY_FAILURE.
//    (در پروژه 6G ۹۳ مورد از همین باگ پیدا شد.)
//
// ۲) هیچ float و هیچ math. — همه مبالغ ریال و همه مساحت‌ها متر مربع، هر دو
//    int64. ضرایب به صورت «در هزار» (per-mille) نگه داشته می‌شوند و تقسیم
//    همیشه آخرین عمل است تا خطای گرد کردن جمع نشود. math.Pow/Log تضمین
//    بیت‌به‌بیت بین معماری‌ها ندارند.
//
// ۳) هیچ پیمایش map بدون مرتب‌سازی — ترتیب map در Go تصادفی است. هرجا روی
//    مجموعه‌ای حلقه می‌زنیم که در خروجی اثر دارد، اول sort.Strings.
// ---------------------------------------------------------------------------

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/hyperledger/fabric-chaincode-go/pkg/cid"
	"github.com/hyperledger/fabric-contract-api-go/contractapi"
)

// ===========================================================================
// ۱. انواع کاربری — هر کدام یک نوع توکن متراژ دارد
// ===========================================================================

const (
	UseResidential = "RES" // مسکونی
	UseCommercial  = "COM" // تجاری
	UseOffice      = "OFF" // اداری
	UseIndustrial  = "IND" // صنعتی و کارگاهی
	UseGarden      = "GRD" // باغی
	UseAgriculture = "AGR" // کشاورزی
	UseEducational = "EDU" // آموزشی
	UseHealth      = "HLT" // درمانی و بهداشتی
	UseTourism     = "TUR" // گردشگری و اقامتی
	UseSport       = "SPT" // ورزشی
	UseReligious   = "REL" // مذهبی و فرهنگی
	UseMixed       = "MIX" // مختلط
	UseGreen       = "GRN" // فضای سبز عمومی
)

// AllUses ترتیب ثابت دارد — هرجا روی انواع کاربری حلقه می‌زنیم از این
// استفاده می‌شود، نه از پیمایش map.
var AllUses = []string{
	UseResidential, UseCommercial, UseOffice, UseIndustrial, UseGarden,
	UseAgriculture, UseEducational, UseHealth, UseTourism, UseSport,
	UseReligious, UseMixed, UseGreen,
}

var UseTitleFa = map[string]string{
	UseResidential: "مسکونی", UseCommercial: "تجاری", UseOffice: "اداری",
	UseIndustrial: "صنعتی", UseGarden: "باغی", UseAgriculture: "کشاورزی",
	UseEducational: "آموزشی", UseHealth: "درمانی", UseTourism: "گردشگری",
	UseSport: "ورزشی", UseReligious: "مذهبی", UseMixed: "مختلط",
	UseGreen: "فضای سبز",
}

// MintableUse — کاربری‌هایی که اصلاً توکن متراژ برایشان صادر می‌شود.
// باغی، کشاورزی و فضای سبز عمداً غیرقابل انتشارند: طبق قانون حفظ و گسترش
// فضای سبز، در باغ ساخته نمی‌شود. ولی مالک باغ «حق توسعه» دارد و می‌تواند
// آن را در بازار TDR بفروشد — همان سازوکاری که هم باغ را نگه می‌دارد و هم
// برای مالک ارزش می‌سازد، به‌جای اینکه او را به تخلف سوق دهد.
var MintableUse = map[string]bool{
	UseResidential: true, UseCommercial: true, UseOffice: true,
	UseIndustrial: true, UseEducational: true, UseHealth: true,
	UseTourism: true, UseSport: true, UseReligious: true, UseMixed: true,
	UseGarden: false, UseAgriculture: false, UseGreen: false,
}

// UseValuePerMille — ارزش نسبی هر متر مربع کاربری نسبت به مسکونی (۱۰۰۰).
// مبنای تبدیل در بازار انتقال حق توسعه: ۱ متر تجاری = ۳ متر مسکونی.
var UseValuePerMille = map[string]int64{
	UseResidential: 1000, UseCommercial: 3000, UseOffice: 2000,
	UseIndustrial: 800, UseEducational: 600, UseHealth: 700,
	UseTourism: 2200, UseSport: 600, UseReligious: 400, UseMixed: 1600,
	UseGarden: 900, UseAgriculture: 300, UseGreen: 0,
}

// ===========================================================================
// ۲. نقش‌ها و کنترل دسترسی
// ===========================================================================

const (
	RoleRegulator  = "REGULATOR"  // org1 — معاونت شهرسازی و معماری
	RoleDistrict   = "DISTRICT"   // org2 — مناطق یازده‌گانه
	RoleEngineer   = "ENGINEER"   // org3 — نظام مهندسی
	RoleCitizen    = "CITIZEN"    // org4 — مالک / دفتر پیشخوان
	RoleFinance    = "FINANCE"    // org5 — مالی و بانک عامل
	RoleCommission = "COMMISSION" // org6 — کمیسیون ماده ۵ و ۱۰۰
	RoleUtility    = "UTILITY"    // org7 — دستگاه‌های استعلام‌شونده
	RoleAuditor    = "AUDITOR"    // org8 — بازرسی و شفافیت
)

// MSPRole — نگاشت پیش‌فرض MSP به نقش. اگر گواهی کاربر ویژگی `role` داشته
// باشد آن اولویت دارد؛ این نگاشت کف کار است تا شبکه بدون صدور گواهی
// ویژگی‌دار هم قابل استفاده باشد.
var MSPRole = map[string]string{
	"org1MSP": RoleRegulator, "org2MSP": RoleDistrict,
	"org3MSP": RoleEngineer, "org4MSP": RoleCitizen,
	"org5MSP": RoleFinance, "org6MSP": RoleCommission,
	"org7MSP": RoleUtility, "org8MSP": RoleAuditor,
}

// ===========================================================================
// ۳. وضعیت‌های پروانه — ماشین حالت
// ===========================================================================

const (
	StDraft        = "DRAFT"        // تشکیل پرونده
	StInquiry      = "INQUIRY"      // استعلامات در جریان
	StDesign       = "DESIGN"       // بررسی و تأیید نقشه
	StCommission5  = "COMMISSION5"  // ارجاع به کمیسیون ماده ۵ (تغییر کاربری/تراکم)
	StAppraisal    = "APPRAISAL"    // محاسبه عوارض
	StPayment      = "PAYMENT"      // در انتظار پرداخت یا اقساط
	StIssued       = "ISSUED"       // پروانه صادر شد
	StFoundation   = "FOUNDATION"   // بازدید مرحله فونداسیون
	StSkeleton     = "SKELETON"     // بازدید مرحله اسکلت
	StFinishing    = "FINISHING"    // بازدید مرحله نازک‌کاری
	StCompletion   = "COMPLETION"   // پایان‌کار صادر شد
	StCommission100 = "COMMISSION100" // ارجاع به کمیسیون ماده ۱۰۰
	StSuspended    = "SUSPENDED"    // متوقف
	StRejected     = "REJECTED"     // رد
	StExpired      = "EXPIRED"      // منقضی
)

// allowedTransitions — گذارهای مجاز و نقشی که اجازه انجامش را دارد.
// کلید: "از→به". هر گذار خارج از این جدول رد می‌شود، حتی از سوی شهردار.
var allowedTransitions = map[string][]string{
	StDraft + "→" + StInquiry:          {RoleDistrict, RoleRegulator},
	StInquiry + "→" + StDesign:         {RoleDistrict, RoleRegulator},
	StInquiry + "→" + StRejected:       {RoleDistrict, RoleRegulator},
	StDesign + "→" + StAppraisal:       {RoleEngineer},
	StDesign + "→" + StCommission5:     {RoleDistrict, RoleRegulator},
	StCommission5 + "→" + StAppraisal:  {RoleCommission},
	StCommission5 + "→" + StRejected:   {RoleCommission},
	StAppraisal + "→" + StPayment:      {RoleFinance, RoleRegulator},
	StPayment + "→" + StIssued:         {RoleFinance, RoleRegulator},
	StIssued + "→" + StFoundation:      {RoleEngineer, RoleDistrict},
	StFoundation + "→" + StSkeleton:    {RoleEngineer, RoleDistrict},
	StSkeleton + "→" + StFinishing:     {RoleEngineer, RoleDistrict},
	StFinishing + "→" + StCompletion:   {RoleDistrict, RoleRegulator},
	StIssued + "→" + StCommission100:      {RoleDistrict, RoleRegulator, RoleAuditor},
	StFoundation + "→" + StCommission100:  {RoleDistrict, RoleRegulator, RoleAuditor},
	StSkeleton + "→" + StCommission100:    {RoleDistrict, RoleRegulator, RoleAuditor},
	StFinishing + "→" + StCommission100:   {RoleDistrict, RoleRegulator, RoleAuditor},
	StCommission100 + "→" + StFinishing:   {RoleCommission},
	StCommission100 + "→" + StCompletion:  {RoleCommission},
	StCommission100 + "→" + StRejected:    {RoleCommission},
	StIssued + "→" + StSuspended:       {RoleRegulator, RoleAuditor},
	StSuspended + "→" + StIssued:       {RoleRegulator},
	StIssued + "→" + StExpired:         {RoleRegulator, RoleDistrict},
}

// ===========================================================================
// ۴. پیشوندهای کلید دفتر
// ===========================================================================

const (
	KeyRegion     = "REGION"  // REGION~<کد منطقه>
	KeyZone       = "ZONE"    // ZONE~<کد پهنه>
	KeyTariff     = "TARIFF"  // TARIFF~<سال>
	KeyParcel     = "PARCEL"  // PARCEL~<پلاک ثبتی>
	KeyPermit     = "PERMIT"  // PERMIT~<شناسه پروانه>
	KeyBalance    = "BAL"     // BAL~<کاربری>~<دارنده>
	KeyEscrow     = "ESC"     // ESC~<پروانه>~<کاربری>
	KeySupply     = "SUP"     // SUP~<کاربری>
	KeyQuota      = "QUOTA"   // QUOTA~<منطقه>~<کاربری>
	KeyInvoice    = "INV"     // INV~<شناسه پروانه>
	KeyReceipt    = "RCP"     // RCP~<شناسه رسید>
	KeyViolation  = "VIO"     // VIO~<شناسه تخلف>
	KeyInquiry    = "INQ"     // INQ~<پروانه>~<دستگاه>
	KeyOffer      = "OFFER"   // OFFER~<شناسه عرضه>
	KeyAudit      = "AUDIT"   // AUDIT~<شناسه رویداد>
	KeyRevenue    = "REV"     // REV~<سال>~<سرفصل>
)

// PDCOwner — مجموعه داده خصوصی برای هویت مالک. کد ملی، نشانی و شماره تماس
// روی کانالی که هشت سازمان عضو آن هستند نباید در دفتر عمومی بنشیند؛ فقط
// هش آن روی زنجیره می‌رود و اصل داده در این مجموعه می‌ماند.
const PDCOwner = "ownerPII"

// PDCFinance — جزئیات مالی (شماره حساب، جدول اقساط) فقط برای سازمان مالی
// و بازرسی.
const PDCFinance = "financeDetail"

// ===========================================================================
// ۵. ساختارهای داده
// ===========================================================================

// Region — یک منطقه از مناطق یازده‌گانه شهرداری شیراز.
type Region struct {
	DocType     string           `json:"docType"`
	Code        string           `json:"code"`        // "1" .. "11"
	Title       string           `json:"title"`       // نام منطقه
	AreaHectare int64            `json:"areaHectare"` // مساحت به هکتار
	// PriceZonal — قیمت منطقه‌ای (P) به ریال بر متر مربع، به تفکیک کاربری.
	// مبنای همه فرمول‌های عوارض است و سالانه توسط کمیسیون تقویم املاک
	// تعیین می‌شود.
	PriceZonal map[string]int64 `json:"priceZonal"`
	UpdatedAt  int64            `json:"updatedAt"`
}

// Zone — پهنه طرح تفصیلی. مشخص می‌کند روی یک پلاک چه چیزی و چقدر می‌شود ساخت.
type Zone struct {
	DocType string `json:"docType"`
	Code    string `json:"code"`
	Region  string `json:"region"`
	Title   string `json:"title"`
	// AllowedUses — کاربری‌های مجاز در این پهنه.
	AllowedUses []string `json:"allowedUses"`
	// FarPerMille — سطح اشغال کل مجاز (Floor Area Ratio) در هزار.
	// ۲۴۰۰ یعنی ۲۴۰٪ زیربنا نسبت به مساحت عرصه.
	FarPerMille int64 `json:"farPerMille"`
	// CoveragePerMille — سطح اشغال همکف در هزار (۶۰۰ = ۶۰٪).
	CoveragePerMille int64 `json:"coveragePerMille"`
	MaxFloors        int64 `json:"maxFloors"`
	MinLotArea       int64 `json:"minLotArea"`       // حداقل مساحت قابل ساخت
	SetbackFrontCm   int64 `json:"setbackFrontCm"`   // عقب‌نشینی از بر گذر (سانتی‌متر)
	// HeritageBuffer — داخل حریم آثار تاریخی؟ استعلام میراث اجباری می‌شود.
	HeritageBuffer bool  `json:"heritageBuffer"`
	// FaultBuffer — روی گسل یا حریم آن؟ سقف طبقات کاهش می‌یابد.
	FaultBuffer bool  `json:"faultBuffer"`
	// TdrSending — پهنه فرستنده حق توسعه (باغات، بافت تاریخی).
	TdrSending bool `json:"tdrSending"`
	// TdrReceiving — پهنه گیرنده حق توسعه (محورهای توسعه‌ای).
	TdrReceiving   bool  `json:"tdrReceiving"`
	TdrMaxPerMille int64 `json:"tdrMaxPerMille"` // سقف تراکم انتقالی نسبت به مجاز
	UpdatedAt      int64 `json:"updatedAt"`
}

// Tariff — دفترچه تعرفه عوارض یک سال. همه ضرایب «در هزار» ذخیره می‌شوند:
// ۵۰۰۰ یعنی ۵ برابر P.
type Tariff struct {
	DocType string `json:"docType"`
	Year    int64  `json:"year"` // سال شمسی، مثلاً ۱۴۰۵
	// BasePerMille — ضریب عوارض زیربنای مجاز، به تفکیک کاربری (ضریب × P).
	BasePerMille map[string]int64 `json:"basePerMille"`
	// ExcessPerMille — ضریب عوارض تراکم مازاد (بعد از تصویب کمیسیون ماده ۵).
	ExcessPerMille map[string]int64 `json:"excessPerMille"`
	// BalconyPerMille — عوارض بالکن و پیش‌آمدگی.
	BalconyPerMille map[string]int64 `json:"balconyPerMille"`
	// FrontagePerMille — ضریب پذیره تجاری/اداری (M در فرمول پذیره).
	FrontagePerMille map[string]int64 `json:"frontagePerMille"`
	// NoParkingPerMille — عوارض حذف پارکینگ برای هر واحد.
	NoParkingPerMille map[string]int64 `json:"noParkingPerMille"`
	// SubdivPerMille — عوارض تفکیک اعیانی (مسکونی ۱۰۰، غیرمسکونی ۲۰۰ = ۱۰٪ و ۲۰٪).
	SubdivPerMille map[string]int64 `json:"subdivPerMille"`
	// SupervisionPerMille — حق‌النظاره مهندس ناظر، درصدی از عوارض کل.
	SupervisionPerMille int64 `json:"supervisionPerMille"`
	// StandardSpanCm / StandardHeightCm — L0 و H0 در فرمول پذیره.
	StandardSpanCm   int64 `json:"standardSpanCm"`   // ۳۰۰ سانتی‌متر
	StandardHeightCm int64 `json:"standardHeightCm"` // ۴۰۰ سانتی‌متر
	// CashDiscountPerMille — تخفیف پرداخت نقدی یکجا.
	CashDiscountPerMille int64 `json:"cashDiscountPerMille"`
	// WornTextureDiscountPerMille — تخفیف نوسازی بافت فرسوده.
	WornTextureDiscountPerMille int64 `json:"wornTextureDiscountPerMille"`
	// GreenBuildingDiscountPerMille — تخفیف ساختمان دارای گواهی انرژی.
	GreenBuildingDiscountPerMille int64 `json:"greenBuildingDiscountPerMille"`
	// LatePenaltyPerMilleMonthly — جریمه دیرکرد ماهانه روی مانده بدهی.
	LatePenaltyPerMilleMonthly int64 `json:"latePenaltyPerMilleMonthly"`
	// DelayFeePerMilleYearly — عوارض تأخیر در اتمام عملیات ساختمانی.
	DelayFeePerMilleYearly int64 `json:"delayFeePerMilleYearly"`
	// TdrFeePerMille — کارمزد شهرداری از هر معامله انتقال حق توسعه.
	TdrFeePerMille int64 `json:"tdrFeePerMille"`
	// Article100MinPerMille / Max — بازه جریمه ماده ۱۰۰: قانون می‌گوید جریمه
	// نباید از یک‌دوم کمتر و از سه برابر ارزش معاملاتی هر متر بنای اضافی
	// بیشتر باشد. یعنی ۵۰۰ تا ۳۰۰۰ در هزار.
	Article100MinPerMille int64 `json:"article100MinPerMille"`
	Article100MaxPerMille int64 `json:"article100MaxPerMille"`
	// FastTrackMaxArea — سقف زیربنا برای صدور خودکار (متر مربع).
	FastTrackMaxArea int64 `json:"fastTrackMaxArea"`
	// InquirySlaDays — مهلت پاسخ استعلام؛ پس از آن تأیید ضمنی اعمال می‌شود.
	InquirySlaDays int64 `json:"inquirySlaDays"`
	UpdatedAt      int64 `json:"updatedAt"`
}

// Parcel — پلاک ثبتی.
type Parcel struct {
	DocType string `json:"docType"`
	ID      string `json:"id"`     // پلاک ثبتی، مثلاً "2345/17"
	Region  string `json:"region"` // کد منطقه ۱ تا ۱۱
	Zone    string `json:"zone"`   // کد پهنه طرح تفصیلی
	// OwnerID — شناسه مالک؛ هویت کامل در PDC ownerPII است.
	OwnerID      string `json:"ownerId"`
	OwnerPIIHash string `json:"ownerPiiHash"`
	LandArea     int64  `json:"landArea"`     // مساحت عرصه (متر مربع)
	ExistingArea int64  `json:"existingArea"` // زیربنای موجود
	FrontageCm   int64  `json:"frontageCm"`   // بر گذر (سانتی‌متر)
	StreetWidthCm int64 `json:"streetWidthCm"`
	CurrentUse   string `json:"currentUse"`
	// SetbackArea — مساحت در طرح تعریض که باید عقب‌نشینی شود.
	SetbackArea int64 `json:"setbackArea"`
	// WornTexture — واقع در بافت فرسوده (مشمول تخفیف نوسازی).
	WornTexture bool `json:"wornTexture"`
	// IsGarden — پلاک باغی. مبنای صدور حق توسعه قابل فروش.
	IsGarden bool `json:"isGarden"`
	// TreeCount — تعداد درخت؛ در محاسبه حق توسعه باغ اثر دارد.
	TreeCount  int64 `json:"treeCount"`
	Blocked    bool  `json:"blocked"` // بازداشت ثبتی یا معارض
	BlockNote  string `json:"blockNote"`
	CreatedAt  int64 `json:"createdAt"`
	UpdatedAt  int64 `json:"updatedAt"`
}

// FloorSpec — یک طبقه از ساختمان درخواستی.
type FloorSpec struct {
	Level    int64  `json:"level"`    // ‑۲، ‑۱، ۰، ۱، ...
	Use      string `json:"use"`      // کاربری همان طبقه (ساختمان مختلط)
	Area     int64  `json:"area"`     // زیربنای ناخالص (متر مربع)
	Units    int64  `json:"units"`    // تعداد واحد
	SpanCm   int64  `json:"spanCm"`   // دهنه (فقط تجاری/اداری)
	HeightCm int64  `json:"heightCm"` // ارتفاع
	Balcony  int64  `json:"balcony"`  // مساحت بالکن و پیش‌آمدگی
}

// Permit — پرونده پروانه ساختمانی.
type Permit struct {
	DocType   string      `json:"docType"`
	ID        string      `json:"id"`
	ParcelID  string      `json:"parcelId"`
	Region    string      `json:"region"`
	Zone      string      `json:"zone"`
	OwnerID   string      `json:"ownerId"`
	Status    string      `json:"status"`
	Floors    []FloorSpec `json:"floors"`
	// TotalArea — مجموع زیربنای درخواستی.
	TotalArea int64 `json:"totalArea"`
	// AllowedArea — زیربنای مجاز طبق پهنه (عرصه × FAR).
	AllowedArea int64 `json:"allowedArea"`
	// ExcessArea — مازاد بر تراکم مجاز. اگر > ۰ باشد یا باید با توکن TDR
	// پوشش داده شود یا به کمیسیون ماده ۵ برود.
	ExcessArea int64 `json:"excessArea"`
	// TdrCoveredArea — متراژی از مازاد که با توکن خریداری‌شده پوشش داده شده.
	TdrCoveredArea int64 `json:"tdrCoveredArea"`
	ParkingRequired int64 `json:"parkingRequired"`
	ParkingProvided int64 `json:"parkingProvided"`
	GreenCertified  bool  `json:"greenCertified"`
	EngineerID      string `json:"engineerId"`   // مهندس ناظر
	DesignerID      string `json:"designerId"`   // طراح
	InvoiceTotal    int64  `json:"invoiceTotal"` // ریال
	PaidTotal       int64  `json:"paidTotal"`
	// TokenLocked — توکن متراژ قفل‌شده به تفکیک کاربری.
	TokenLocked map[string]int64 `json:"tokenLocked"`
	FastTracked bool             `json:"fastTracked"`
	IssuedAt    int64            `json:"issuedAt"`
	ExpiresAt   int64            `json:"expiresAt"`
	// BuiltArea — متراژ واقعی گزارش‌شده در بازدیدها؛ مبنای کشف تخلف.
	BuiltArea int64 `json:"builtArea"`
	History   []Transition `json:"history"`
	CreatedAt int64        `json:"createdAt"`
	UpdatedAt int64        `json:"updatedAt"`
}

// Transition — یک گام در تاریخچه پرونده. تاریخچه روی خود سند می‌ماند تا
// یک query کامل باشد و نیازی به پیمایش بلاک‌ها نباشد.
type Transition struct {
	From  string `json:"from"`
	To    string `json:"to"`
	By    string `json:"by"`
	MSP   string `json:"msp"`
	Note  string `json:"note"`
	At    int64  `json:"at"`
	TxID  string `json:"txId"`
}

// FeeLine — یک ردیف از صورت‌حساب عوارض.
type FeeLine struct {
	Code   string `json:"code"`
	Title  string `json:"title"`
	Basis  string `json:"basis"`  // شرح مبنای محاسبه، برای شفافیت
	Amount int64  `json:"amount"` // ریال
}

// Invoice — صورت‌حساب عوارض یک پروانه.
type Invoice struct {
	DocType    string    `json:"docType"`
	PermitID   string    `json:"permitId"`
	TariffYear int64     `json:"tariffYear"`
	Lines      []FeeLine `json:"lines"`
	Subtotal   int64     `json:"subtotal"`
	Discount   int64     `json:"discount"`
	Total      int64     `json:"total"`
	Paid       int64     `json:"paid"`
	Penalty    int64     `json:"penalty"`
	Settled    bool      `json:"settled"`
	// Installments — جدول اقساط؛ جزئیاتش در PDC مالی هم آینه می‌شود.
	Installments []Installment `json:"installments"`
	CreatedAt    int64         `json:"createdAt"`
	UpdatedAt    int64         `json:"updatedAt"`
}

type Installment struct {
	No     int64 `json:"no"`
	DueAt  int64 `json:"dueAt"`
	Amount int64 `json:"amount"`
	Paid   int64 `json:"paid"`
}

// ===========================================================================
// ۶. پایه مشترک قراردادها
// ===========================================================================

// NetworkBase پایه‌ای است که همه قراردادها آن را embed می‌کنند.
type NetworkBase struct {
	contractapi.Contract
}

// --------------------------- زمان و هویت ---------------------------

// txTime زمان تراکنش را برمی‌گرداند — تنها منبع مجاز زمان در این شبکه.
func txTime(ctx contractapi.TransactionContextInterface) int64 {
	ts, err := ctx.GetStub().GetTxTimestamp()
	if err != nil || ts == nil {
		return 0
	}
	return ts.Seconds
}

// callerMSP شناسه MSP فراخوان.
func callerMSP(ctx contractapi.TransactionContextInterface) string {
	msp, err := cid.GetMSPID(ctx.GetStub())
	if err != nil {
		return ""
	}
	return msp
}

// callerID شناسه یکتای فراخوان. اگر گواهی ویژگی `uid` داشته باشد از آن،
// وگرنه از CN گواهی استخراج می‌شود.
func callerID(ctx contractapi.TransactionContextInterface) string {
	if v, ok, _ := cid.GetAttributeValue(ctx.GetStub(), "uid"); ok && v != "" {
		return v
	}
	id, err := cid.GetID(ctx.GetStub())
	if err != nil {
		return "unknown"
	}
	return shortHash(id)
}

// callerRole نقش فراخوان. ویژگی گواهی اولویت دارد؛ اگر نبود از MSP.
func callerRole(ctx contractapi.TransactionContextInterface) string {
	if v, ok, _ := cid.GetAttributeValue(ctx.GetStub(), "role"); ok && v != "" {
		return strings.ToUpper(v)
	}
	return MSPRole[callerMSP(ctx)]
}

// requireRole اجازه را بررسی می‌کند و در صورت نبود، خطای گویا می‌دهد.
func requireRole(ctx contractapi.TransactionContextInterface, roles ...string) error {
	got := callerRole(ctx)
	for _, r := range roles {
		if got == r {
			return nil
		}
	}
	sorted := append([]string{}, roles...)
	sort.Strings(sorted)
	return fmt.Errorf("دسترسی مجاز نیست: نقش شما «%s» است، این عملیات نیاز به یکی از این نقش‌ها دارد: %s",
		got, strings.Join(sorted, "، "))
}

// canTransition بررسی می‌کند گذار وضعیت مجاز است و فراخوان اجازه‌اش را دارد.
func canTransition(ctx contractapi.TransactionContextInterface, from, to string) error {
	roles, ok := allowedTransitions[from+"→"+to]
	if !ok {
		return fmt.Errorf("گذار غیرمجاز: از «%s» نمی‌توان مستقیم به «%s» رفت", from, to)
	}
	return requireRole(ctx, roles...)
}

// --------------------------- دفتر ---------------------------

func mkKey(parts ...string) string { return strings.Join(parts, "~") }

func putJSON(ctx contractapi.TransactionContextInterface, key string, v interface{}) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("خطای سریال‌سازی %s: %v", key, err)
	}
	return ctx.GetStub().PutState(key, b)
}

func getJSON(ctx contractapi.TransactionContextInterface, key string, v interface{}) (bool, error) {
	b, err := ctx.GetStub().GetState(key)
	if err != nil {
		return false, fmt.Errorf("خطای خواندن %s: %v", key, err)
	}
	if b == nil {
		return false, nil
	}
	if err := json.Unmarshal(b, v); err != nil {
		return false, fmt.Errorf("خطای واسازی %s: %v", key, err)
	}
	return true, nil
}

func mustGetJSON(ctx contractapi.TransactionContextInterface, key string, v interface{}, what string) error {
	ok, err := getJSON(ctx, key, v)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s با شناسه «%s» یافت نشد", what, key)
	}
	return nil
}

// queryByPrefix همه اسناد با یک پیشوند کلید را برمی‌گرداند.
// نتیجه به ترتیب کلید است (GetStateByRange مرتب است) — پس قطعی است.
func queryByPrefix(ctx contractapi.TransactionContextInterface, prefix string) ([]json.RawMessage, error) {
	it, err := ctx.GetStub().GetStateByRange(prefix+"~", prefix+"~\uffff")
	if err != nil {
		return nil, err
	}
	defer it.Close()
	out := []json.RawMessage{}
	for it.HasNext() {
		kv, err := it.Next()
		if err != nil {
			return nil, err
		}
		out = append(out, json.RawMessage(kv.Value))
	}
	return out, nil
}

// --------------------------- رویداد و حسابرسی ---------------------------

// AuditEvent — رویدادی که هم به‌عنوان event منتشر و هم در دفتر شفافیت ثبت می‌شود.
type AuditEvent struct {
	Kind    string `json:"kind"`
	Subject string `json:"subject"`
	Actor   string `json:"actor"`
	MSP     string `json:"msp"`
	Role    string `json:"role"`
	Amount  int64  `json:"amount,omitempty"`
	Detail  string `json:"detail"`
	At      int64  `json:"at"`
	TxID    string `json:"txId"`
}

// emit یک رویداد را منتشر می‌کند. لایه API با همین رویدادها داشبورد را
// بی‌درنگ به‌روز می‌کند — نه با polling روی دفتر.
func emit(ctx contractapi.TransactionContextInterface, kind, subject, detail string, amount int64) error {
	ev := AuditEvent{
		Kind: kind, Subject: subject, Actor: callerID(ctx),
		MSP: callerMSP(ctx), Role: callerRole(ctx), Amount: amount,
		Detail: detail, At: txTime(ctx), TxID: ctx.GetStub().GetTxID(),
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	return ctx.GetStub().SetEvent(kind, b)
}

// --------------------------- ابزار ---------------------------

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

// hashPII هش داده هویتی برای درج در دفتر عمومی. اصل داده در PDC می‌ماند.
func hashPII(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// perMille ضرب در ضریب هزارم با تقسیم در انتها.
// این تابع تنها جایی است که تقسیم انجام می‌شود — همه فرمول‌ها از آن عبور
// می‌کنند تا رفتار گرد کردن یکسان و قابل بازتولید باشد.
func perMille(value, coeff int64) int64 {
	return (value * coeff) / 1000
}

// ratioPerMille نسبت a/b را به‌صورت هزارم می‌دهد؛ اگر b صفر باشد ۱۰۰۰
// (یعنی نسبت ۱) برمی‌گردد تا فرمول پذیره روی داده ناقص نترکد.
func ratioPerMille(a, b int64) int64 {
	if b <= 0 {
		return 1000
	}
	return (a * 1000) / b
}

// clampInt64 مقدار را در بازه نگه می‌دارد.
func clampInt64(v, lo, hi int64) int64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func atoi64(s string) (int64, error) {
	v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("عدد نامعتبر: «%s»", s)
	}
	return v, nil
}

// secondsPerDay / Month / Year — ثابت‌های تقویمی. سال ۳۶۵ روزه در نظر
// گرفته شده؛ برای محاسبه جریمه دیرکرد و انقضای پروانه کافی است و قطعی
// می‌ماند (تبدیل شمسی روی زنجیره انجام نمی‌شود — کار لایه نمایش است).
const (
	SecondsPerDay   int64 = 86400
	SecondsPerMonth int64 = 2592000
	SecondsPerYear  int64 = 31536000
)

// ===========================================================================
// ۷. دفتر توکن متراژ — یک متر مربع = یک توکن
// ===========================================================================
//
// این هسته اقتصادی شبکه است. توکن‌ها fungible و به تفکیک کاربری‌اند
// (SQM-RES، SQM-COM، ...). سه عملیات پایه: انتشار، انتقال، سوزاندن؛ به‌علاوه
// قفل در وثیقه که برای صدور پروانه لازم است.
//
// چرا اصلاً توکن؟ چون بدون آن، «تراکم» یک عدد در یک فرم اداری است که هر بار
// از نو تفسیر می‌شود. با توکن، تراکم یک دارایی کمیاب و قابل‌ردیابی است:
// سقف انتشار هر منطقه در طرح تفصیلی قفل می‌شود، هر متر ساخته‌شده یک توکن
// می‌سوزاند، و هر متر ناساخته قابل فروش است. کمیابی قابل اثبات، هم بازار
// می‌سازد و هم تخلف را قابل کشف می‌کند.

func balKey(use, holder string) string { return mkKey(KeyBalance, use, holder) }
func escKey(permitID, use string) string { return mkKey(KeyEscrow, permitID, use) }
func supKey(use string) string          { return mkKey(KeySupply, use) }
func quotaKey(region, use string) string { return mkKey(KeyQuota, region, use) }

// Quota — سقف انتشار توکن یک کاربری در یک منطقه. طرح تفصیلی این عدد را
// تعیین می‌کند؛ بدون آن انتشار توکن بی‌معنا می‌شود چون کمیابی ندارد.
type Quota struct {
	DocType   string `json:"docType"`
	Region    string `json:"region"`
	Use       string `json:"use"`
	Ceiling   int64  `json:"ceiling"`   // سقف کل (متر مربع)
	Minted    int64  `json:"minted"`    // منتشرشده
	Burned    int64  `json:"burned"`    // سوخته (ساخته‌شده)
	UpdatedAt int64  `json:"updatedAt"`
}

func readBalance(ctx contractapi.TransactionContextInterface, use, holder string) (int64, error) {
	b, err := ctx.GetStub().GetState(balKey(use, holder))
	if err != nil {
		return 0, err
	}
	if b == nil {
		return 0, nil
	}
	return atoi64(string(b))
}

func writeBalance(ctx contractapi.TransactionContextInterface, use, holder string, v int64) error {
	if v < 0 {
		return fmt.Errorf("موجودی منفی مجاز نیست")
	}
	if v == 0 {
		return ctx.GetStub().DelState(balKey(use, holder))
	}
	return ctx.GetStub().PutState(balKey(use, holder), []byte(strconv.FormatInt(v, 10)))
}

func addBalance(ctx contractapi.TransactionContextInterface, use, holder string, delta int64) error {
	cur, err := readBalance(ctx, use, holder)
	if err != nil {
		return err
	}
	next := cur + delta
	if next < 0 {
		return fmt.Errorf("موجودی توکن %s برای «%s» کافی نیست: موجود %d، نیاز %d",
			UseTitleFa[use], holder, cur, -delta)
	}
	return writeBalance(ctx, use, holder, next)
}

func readSupply(ctx contractapi.TransactionContextInterface, use string) (int64, error) {
	b, err := ctx.GetStub().GetState(supKey(use))
	if err != nil || b == nil {
		return 0, err
	}
	return atoi64(string(b))
}

func addSupply(ctx contractapi.TransactionContextInterface, use string, delta int64) error {
	cur, err := readSupply(ctx, use)
	if err != nil {
		return err
	}
	if cur+delta < 0 {
		return fmt.Errorf("عرضه کل نمی‌تواند منفی شود")
	}
	return ctx.GetStub().PutState(supKey(use), []byte(strconv.FormatInt(cur+delta, 10)))
}

// mintTokens انتشار توکن با رعایت سقف منطقه.
//
// ⚠️ نکته کلید داغ: رکورد Quota هر بار خوانده و بازنوشته می‌شود. اگر همه
// پروانه‌های یک منطقه هم‌زمان بیایند، همان الگوی MVCC_READ_CONFLICT پروژه
// 6G تکرار می‌شود. اینجا قابل قبول است چون انتشار عملیات کم‌بسامدی است
// (سالی یک‌بار برای هر منطقه، توسط شهرداری)، نه مسیر داغ تراکنش. مسیر داغ
// قفل و آزادسازی وثیقه است که کلیدش به شناسه پروانه مقید است و رقابتی ندارد.
func mintTokens(ctx contractapi.TransactionContextInterface, region, use, holder string, amount int64) error {
	if amount <= 0 {
		return fmt.Errorf("مقدار انتشار باید مثبت باشد")
	}
	if !MintableUse[use] {
		return fmt.Errorf("کاربری «%s» قابل انتشار توکن نیست؛ حق توسعه آن فقط از طریق بازار انتقال قابل واگذاری است",
			UseTitleFa[use])
	}
	var q Quota
	ok, err := getJSON(ctx, quotaKey(region, use), &q)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("سقف انتشار برای منطقه %s و کاربری %s تعریف نشده است", region, UseTitleFa[use])
	}
	if q.Minted+amount > q.Ceiling {
		return fmt.Errorf("انتشار %d متر از سقف منطقه %s فراتر می‌رود (سقف %d، منتشرشده %d، ظرفیت باقی‌مانده %d)",
			amount, region, q.Ceiling, q.Minted, q.Ceiling-q.Minted)
	}
	q.Minted += amount
	q.UpdatedAt = txTime(ctx)
	if err := putJSON(ctx, quotaKey(region, use), &q); err != nil {
		return err
	}
	if err := addBalance(ctx, use, holder, amount); err != nil {
		return err
	}
	return addSupply(ctx, use, amount)
}

// lockTokens قفل کردن توکن در وثیقه یک پروانه.
func lockTokens(ctx contractapi.TransactionContextInterface, permitID, use, holder string, amount int64) error {
	if amount <= 0 {
		return nil
	}
	if err := addBalance(ctx, use, holder, -amount); err != nil {
		return err
	}
	have, err := readEscrow(ctx, permitID, use)
	if err != nil {
		return err
	}
	return ctx.GetStub().PutState(escKey(permitID, use),
		[]byte(strconv.FormatInt(have+amount, 10)))
}

func readEscrow(ctx contractapi.TransactionContextInterface, permitID, use string) (int64, error) {
	b, err := ctx.GetStub().GetState(escKey(permitID, use))
	if err != nil || b == nil {
		return 0, err
	}
	return atoi64(string(b))
}

// burnEscrow سوزاندن توکن قفل‌شده — هنگام پایان‌کار. متری که ساخته شد دیگر
// قابل معامله نیست.
func burnEscrow(ctx contractapi.TransactionContextInterface, permitID, region, use string, amount int64) error {
	have, err := readEscrow(ctx, permitID, use)
	if err != nil {
		return err
	}
	if amount > have {
		return fmt.Errorf("توکن قفل‌شده کافی نیست: قفل %d، درخواست سوزاندن %d", have, amount)
	}
	rest := have - amount
	if rest == 0 {
		if err := ctx.GetStub().DelState(escKey(permitID, use)); err != nil {
			return err
		}
	} else if err := ctx.GetStub().PutState(escKey(permitID, use),
		[]byte(strconv.FormatInt(rest, 10))); err != nil {
		return err
	}
	if err := addSupply(ctx, use, -amount); err != nil {
		return err
	}
	var q Quota
	ok, err := getJSON(ctx, quotaKey(region, use), &q)
	if err != nil {
		return err
	}
	if ok {
		q.Burned += amount
		q.UpdatedAt = txTime(ctx)
		if err := putJSON(ctx, quotaKey(region, use), &q); err != nil {
			return err
		}
	}
	return nil
}

// releaseEscrow بازگرداندن توکن قفل‌شده به مالک — هنگام ابطال یا انقضای پروانه.
func releaseEscrow(ctx contractapi.TransactionContextInterface, permitID, use, holder string, amount int64) error {
	have, err := readEscrow(ctx, permitID, use)
	if err != nil {
		return err
	}
	if amount > have {
		amount = have
	}
	if amount <= 0 {
		return nil
	}
	rest := have - amount
	if rest == 0 {
		if err := ctx.GetStub().DelState(escKey(permitID, use)); err != nil {
			return err
		}
	} else if err := ctx.GetStub().PutState(escKey(permitID, use),
		[]byte(strconv.FormatInt(rest, 10))); err != nil {
		return err
	}
	return addBalance(ctx, use, holder, amount)
}

// convertUse تبدیل متراژ یک کاربری به معادل کاربری دیگر بر پایه ارزش نسبی.
// مبنای معاملات بازار انتقال حق توسعه.
func convertUse(amount int64, fromUse, toUse string) (int64, error) {
	fv, ok1 := UseValuePerMille[fromUse]
	tv, ok2 := UseValuePerMille[toUse]
	if !ok1 || !ok2 {
		return 0, fmt.Errorf("کاربری نامعتبر در تبدیل")
	}
	if tv <= 0 {
		return 0, fmt.Errorf("کاربری «%s» ارزش تبدیل ندارد", UseTitleFa[toUse])
	}
	return (amount * fv) / tv, nil
}

// ZoningVerdict — نتیجه انطباق یک درخواست با ضوابط پهنه.
type ZoningVerdict struct {
	Compliant     bool     `json:"compliant"`
	AllowedArea   int64    `json:"allowedArea"`
	RequestedArea int64    `json:"requestedArea"`
	ExcessArea    int64    `json:"excessArea"`
	MaxFloors     int64    `json:"maxFloors"`
	UsedFloors    int64    `json:"usedFloors"`
	NeedsHeritage bool     `json:"needsHeritage"`
	Violations    []string `json:"violations"`
	Notes         []string `json:"notes"`
}

// ===========================================================================
// ۸. موتور محاسبه عوارض
// ===========================================================================
//
// همه فرمول‌ها روی صحیح کار می‌کنند و شرح مبنای هر ردیف در خود ردیف ذخیره
// می‌شود. علت: بزرگ‌ترین شکایت شهروند از عوارض «چرا این عدد؟» است. وقتی
// مبنا روی زنجیره باشد، هم شهروند می‌بیند و هم بازرس می‌تواند بازمحاسبه کند.

// FeeInput — ورودی کامل محاسبه.
type FeeInput struct {
	Permit  Permit
	Parcel  Parcel
	Region  Region
	Zone    Zone
	Tariff  Tariff
	CashPay bool
}

// computeFees صورت‌حساب کامل یک پروانه را می‌سازد.
func computeFees(in FeeInput, now int64) Invoice {
	inv := Invoice{
		DocType:    "invoice",
		PermitID:   in.Permit.ID,
		TariffYear: in.Tariff.Year,
		Lines:      []FeeLine{},
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	// --- ردیف ۱: عوارض زیربنای مجاز، طبقه به طبقه ---
	// مبنا: مساحت × ضریب کاربری × قیمت منطقه‌ای. ضریب طبقات بالاتر از سوم
	// ۱۰٪ به ازای هر طبقه اضافه می‌شود (ارزش افزوده ارتفاع).
	remainingAllowed := in.Permit.AllowedArea
	for _, f := range in.Permit.Floors {
		if f.Area <= 0 {
			continue
		}
		p := in.Region.PriceZonal[f.Use]
		if p == 0 {
			p = in.Region.PriceZonal[UseResidential]
		}
		coeff := in.Tariff.BasePerMille[f.Use]
		if coeff == 0 {
			coeff = in.Tariff.BasePerMille[UseResidential]
		}
		if f.Level > 3 {
			coeff += coeff * (f.Level - 3) / 10
		}

		inArea := f.Area
		if inArea > remainingAllowed {
			inArea = remainingAllowed
		}
		if inArea > 0 {
			amt := perMille(inArea*p, coeff)
			inv.Lines = append(inv.Lines, FeeLine{
				Code:  "BASE",
				Title: fmt.Sprintf("عوارض زیربنای مجاز — طبقه %d (%s)", f.Level, UseTitleFa[f.Use]),
				Basis: fmt.Sprintf("%d م² × %d ریال × %d‰", inArea, p, coeff),
				Amount: amt,
			})
			remainingAllowed -= inArea
		}

		// --- ردیف ۲: عوارض تراکم مازاد ---
		over := f.Area - inArea
		if over > 0 {
			ecoeff := in.Tariff.ExcessPerMille[f.Use]
			if ecoeff == 0 {
				ecoeff = in.Tariff.ExcessPerMille[UseResidential]
			}
			amt := perMille(over*p, ecoeff)
			inv.Lines = append(inv.Lines, FeeLine{
				Code:  "EXCESS",
				Title: fmt.Sprintf("عوارض تراکم مازاد — طبقه %d (%s)", f.Level, UseTitleFa[f.Use]),
				Basis: fmt.Sprintf("%d م² مازاد × %d ریال × %d‰", over, p, ecoeff),
				Amount: amt,
			})
		}

		// --- ردیف ۳: پذیره تجاری و اداری ---
		// فرمول متعارف: M% × P × S × (L/L0) × (H/H0) × N
		// همه نسبت‌ها هزارم و در سقف ۳ برابر بریده می‌شوند تا یک دهنه غیرعادی
		// عدد را منفجر نکند.
		if f.Use == UseCommercial || f.Use == UseOffice {
			m := in.Tariff.FrontagePerMille[f.Use]
			if m > 0 {
				rl := clampInt64(ratioPerMille(f.SpanCm, in.Tariff.StandardSpanCm), 1000, 3000)
				rh := clampInt64(ratioPerMille(f.HeightCm, in.Tariff.StandardHeightCm), 1000, 3000)
				units := f.Units
				if units <= 0 {
					units = 1
				}
				amt := perMille(perMille(perMille(f.Area*p, m), rl), rh) * units
				inv.Lines = append(inv.Lines, FeeLine{
					Code:  "FRONTAGE",
					Title: fmt.Sprintf("عوارض پذیره %s — طبقه %d", UseTitleFa[f.Use], f.Level),
					Basis: fmt.Sprintf("%d م² × %d ریال × %d‰ × دهنه %d‰ × ارتفاع %d‰ × %d واحد",
						f.Area, p, m, rl, rh, units),
					Amount: amt,
				})
			}
		}

		// --- ردیف ۴: بالکن و پیش‌آمدگی ---
		if f.Balcony > 0 {
			bc := in.Tariff.BalconyPerMille[f.Use]
			if bc == 0 {
				bc = in.Tariff.BalconyPerMille[UseResidential]
			}
			amt := perMille(f.Balcony*p, bc)
			inv.Lines = append(inv.Lines, FeeLine{
				Code:  "BALCONY",
				Title: fmt.Sprintf("عوارض بالکن و پیش‌آمدگی — طبقه %d", f.Level),
				Basis: fmt.Sprintf("%d م² × %d ریال × %d‰", f.Balcony, p, bc),
				Amount: amt,
			})
		}
	}

	// --- ردیف ۵: حذف پارکینگ ---
	// این ردیف عمداً گران است: هدف تأمین پارکینگ است نه فروش آن. اگر ارزان
	// باشد سازنده می‌خرد و بار ترافیک روی شهر می‌ماند.
	missing := in.Permit.ParkingRequired - in.Permit.ParkingProvided
	if missing > 0 {
		p := in.Region.PriceZonal[UseResidential]
		coeff := in.Tariff.NoParkingPerMille[UseResidential]
		if in.Permit.Floors != nil {
			for _, f := range in.Permit.Floors {
				if f.Use == UseCommercial {
					coeff = in.Tariff.NoParkingPerMille[UseCommercial]
					p = in.Region.PriceZonal[UseCommercial]
					break
				}
			}
		}
		// هر پارکینگ ۲۵ متر مربع فرض می‌شود (ابعاد متعارف با مسیر دسترسی).
		amt := perMille(missing*25*p, coeff)
		inv.Lines = append(inv.Lines, FeeLine{
			Code:  "NOPARK",
			Title: "عوارض کسری پارکینگ",
			Basis: fmt.Sprintf("%d واحد کسری × ۲۵ م² × %d ریال × %d‰", missing, p, coeff),
			Amount: amt,
		})
	}

	// --- ردیف ۶: عوارض تفکیک اعیانی ---
	totalUnits := int64(0)
	nonResArea := int64(0)
	resArea := int64(0)
	for _, f := range in.Permit.Floors {
		totalUnits += f.Units
		if f.Use == UseResidential {
			resArea += f.Area
		} else {
			nonResArea += f.Area
		}
	}
	if totalUnits > 1 {
		if resArea > 0 {
			p := in.Region.PriceZonal[UseResidential]
			amt := perMille(resArea*p, in.Tariff.SubdivPerMille[UseResidential])
			inv.Lines = append(inv.Lines, FeeLine{
				Code: "SUBDIV", Title: "عوارض تفکیک اعیانی مسکونی",
				Basis:  fmt.Sprintf("%d م² × %d ریال × %d‰", resArea, p, in.Tariff.SubdivPerMille[UseResidential]),
				Amount: amt,
			})
		}
		if nonResArea > 0 {
			p := in.Region.PriceZonal[UseCommercial]
			if p == 0 {
				p = in.Region.PriceZonal[UseResidential]
			}
			amt := perMille(nonResArea*p, in.Tariff.SubdivPerMille[UseCommercial])
			inv.Lines = append(inv.Lines, FeeLine{
				Code: "SUBDIV", Title: "عوارض تفکیک اعیانی غیرمسکونی",
				Basis:  fmt.Sprintf("%d م² × %d ریال × %d‰", nonResArea, p, in.Tariff.SubdivPerMille[UseCommercial]),
				Amount: amt,
			})
		}
	}

	// --- جمع جزء ---
	sub := int64(0)
	for _, l := range inv.Lines {
		sub += l.Amount
	}

	// --- ردیف ۷: حق‌النظاره مهندس ناظر (درصدی از عوارض) ---
	if in.Tariff.SupervisionPerMille > 0 {
		amt := perMille(sub, in.Tariff.SupervisionPerMille)
		inv.Lines = append(inv.Lines, FeeLine{
			Code: "SUPERVISION", Title: "حق‌النظاره مهندس ناظر",
			Basis:  fmt.Sprintf("%d‰ از جمع عوارض", in.Tariff.SupervisionPerMille),
			Amount: amt,
		})
		sub += amt
	}
	inv.Subtotal = sub

	// --- تخفیف‌ها ---
	// تخفیف ابزار سیاست‌گذاری است، نه لطف: نوسازی بافت فرسوده و ساختمان سبز
	// هزینه بلندمدت شهر را کم می‌کنند، پس تخفیفشان سرمایه‌گذاری است.
	disc := int64(0)
	if in.Parcel.WornTexture && in.Tariff.WornTextureDiscountPerMille > 0 {
		disc += perMille(sub, in.Tariff.WornTextureDiscountPerMille)
	}
	if in.Permit.GreenCertified && in.Tariff.GreenBuildingDiscountPerMille > 0 {
		disc += perMille(sub, in.Tariff.GreenBuildingDiscountPerMille)
	}
	if in.CashPay && in.Tariff.CashDiscountPerMille > 0 {
		disc += perMille(sub, in.Tariff.CashDiscountPerMille)
	}
	// تخفیف کل هرگز از نیمی از عوارض بیشتر نمی‌شود.
	disc = clampInt64(disc, 0, sub/2)
	inv.Discount = disc
	inv.Total = sub - disc
	return inv
}

// article100Penalty جریمه ماده ۱۰۰ برای بنای اضافی.
// قانون بازه می‌دهد (نصف تا سه برابر ارزش معاملاتی هر متر) و انتخاب داخل
// بازه با کمیسیون است. این تابع فقط پیشنهاد می‌سازد: شدت تخلف را از نسبت
// مازاد به مجاز درمی‌آورد و خطی داخل بازه می‌نشاند. رأی نهایی همچنان با
// کمیسیون است — قرارداد آن را جایگزین نمی‌کند، فقط پرونده را آماده می‌کند.
func article100Penalty(excessArea, priceZonal, minPM, maxPM, allowedArea int64) (int64, int64) {
	if excessArea <= 0 {
		return 0, 0
	}
	severity := ratioPerMille(excessArea, allowedArea) // نسبت تخلف در هزار
	severity = clampInt64(severity, 0, 1000)
	pm := minPM + ((maxPM - minPM) * severity / 1000)
	return excessArea * priceZonal * pm / 1000, pm
}
