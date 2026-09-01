package main

// ---------------------------------------------------------------------------
// Regulation — قرارداد مقررات شهرسازی
//
// این قرارداد «قانون» شبکه است: طرح تفصیلی، پهنه‌بندی، قیمت منطقه‌ای، دفترچه
// تعرفه و سقف انتشار تراکم. هیچ قرارداد دیگری مجاز نیست این اعداد را از خودش
// دربیاورد؛ همه از اینجا می‌خوانند.
//
// چرا این جدایی مهم است: در سامانه‌های فعلی، ضوابط در کد برنامه یا در جدول
// یک پایگاه‌داده‌اند که کارشناس می‌تواند بی‌رد پا عوضش کند. اینجا هر تغییر
// ضابطه یک تراکنش امضاشده با تاریخچه است، و چون پروانه‌های قبلی به نسخه‌ای
// از تعرفه ارجاع می‌دهند که در زمان صدور معتبر بوده، تغییر تعرفه گذشته را
// بازنویسی نمی‌کند.
// ---------------------------------------------------------------------------

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"

	"github.com/hyperledger/fabric-contract-api-go/contractapi"
)

type RegulationContract struct {
	NetworkBase
}

// ---------------------------- مناطق ----------------------------

// SetRegion ثبت یا به‌روزرسانی یک منطقه شهرداری و قیمت‌های منطقه‌ای آن.
// priceJSON نگاشت کاربری به ریال بر متر مربع است، مثلاً:
//   {"RES":45000000,"COM":180000000,"OFF":95000000}
func (c *RegulationContract) SetRegion(ctx contractapi.TransactionContextInterface,
	code, title string, areaHectare int64, priceJSON string) error {

	if err := requireRole(ctx, RoleRegulator); err != nil {
		return err
	}
	var prices map[string]int64
	if err := json.Unmarshal([]byte(priceJSON), &prices); err != nil {
		return fmt.Errorf("قیمت منطقه‌ای نامعتبر است: %v", err)
	}
	// کلیدها مرتب می‌شوند تا اگر چند کاربری نامعتبر باشد، همه peerها دقیقاً
	// یک پیام خطا بسازند. با پیمایش مستقیم map، Go ترتیب را تصادفی می‌کند و
	// دو peer دو پیام متفاوت می‌دهند.
	useKeys := make([]string, 0, len(prices))
	for use := range prices {
		useKeys = append(useKeys, use)
	}
	sort.Strings(useKeys)
	for _, use := range useKeys {
		if _, ok := UseTitleFa[use]; !ok {
			return fmt.Errorf("کاربری ناشناخته در قیمت منطقه‌ای: «%s»", use)
		}
		if prices[use] < 0 {
			return fmt.Errorf("قیمت منطقه‌ای منفی برای «%s» مجاز نیست", use)
		}
	}
	r := Region{
		DocType: "region", Code: code, Title: title,
		AreaHectare: areaHectare, PriceZonal: prices, UpdatedAt: txTime(ctx),
	}
	if err := putJSON(ctx, mkKey(KeyRegion, code), &r); err != nil {
		return err
	}
	return emit(ctx, "RegionUpdated", code,
		fmt.Sprintf("منطقه %s (%s) با %d کاربری قیمت‌گذاری شد", code, title, len(prices)), 0)
}

func (c *RegulationContract) GetRegion(ctx contractapi.TransactionContextInterface,
	code string) (*Region, error) {
	var r Region
	if err := mustGetJSON(ctx, mkKey(KeyRegion, code), &r, "منطقه"); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *RegulationContract) ListRegions(ctx contractapi.TransactionContextInterface) ([]Region, error) {
	raw, err := queryByPrefix(ctx, KeyRegion)
	if err != nil {
		return nil, err
	}
	out := []Region{}
	for _, b := range raw {
		var r Region
		if err := json.Unmarshal(b, &r); err == nil {
			out = append(out, r)
		}
	}
	return out, nil
}

// ---------------------------- پهنه‌ها ----------------------------

// SetZone ثبت یک پهنه طرح تفصیلی.
// usesCSV فهرست کاربری‌های مجاز با کاما، مثلاً "RES,COM,MIX".
func (c *RegulationContract) SetZone(ctx contractapi.TransactionContextInterface,
	code, region, title, usesCSV string,
	farPerMille, coveragePerMille, maxFloors, minLotArea, setbackFrontCm int64,
	heritage, fault, tdrSending, tdrReceiving bool, tdrMaxPerMille int64) error {

	if err := requireRole(ctx, RoleRegulator); err != nil {
		return err
	}
	var reg Region
	if err := mustGetJSON(ctx, mkKey(KeyRegion, region), &reg, "منطقه"); err != nil {
		return err
	}
	uses := splitCSV(usesCSV)
	for _, u := range uses {
		if _, ok := UseTitleFa[u]; !ok {
			return fmt.Errorf("کاربری ناشناخته در پهنه: «%s»", u)
		}
	}
	if farPerMille < 0 || coveragePerMille < 0 || coveragePerMille > 1000 {
		return fmt.Errorf("ضرایب تراکم و سطح اشغال نامعتبرند (سطح اشغال باید بین ۰ تا ۱۰۰۰ در هزار باشد)")
	}
	// حریم گسل: سقف طبقات به‌صورت قهری کاهش می‌یابد. این را عمداً در قرارداد
	// گذاشته‌ایم نه در دستورالعمل، چون بندهایی که فقط در دستورالعمل‌اند در
	// عمل دور زده می‌شوند.
	if fault && maxFloors > 4 {
		maxFloors = 4
	}
	z := Zone{
		DocType: "zone", Code: code, Region: region, Title: title,
		AllowedUses: uses, FarPerMille: farPerMille,
		CoveragePerMille: coveragePerMille, MaxFloors: maxFloors,
		MinLotArea: minLotArea, SetbackFrontCm: setbackFrontCm,
		HeritageBuffer: heritage, FaultBuffer: fault,
		TdrSending: tdrSending, TdrReceiving: tdrReceiving,
		TdrMaxPerMille: tdrMaxPerMille, UpdatedAt: txTime(ctx),
	}
	if err := putJSON(ctx, mkKey(KeyZone, code), &z); err != nil {
		return err
	}
	return emit(ctx, "ZoneUpdated", code,
		fmt.Sprintf("پهنه %s در منطقه %s: تراکم %d‰، سطح اشغال %d‰، حداکثر %d طبقه",
			code, region, farPerMille, coveragePerMille, maxFloors), 0)
}

func (c *RegulationContract) GetZone(ctx contractapi.TransactionContextInterface,
	code string) (*Zone, error) {
	var z Zone
	if err := mustGetJSON(ctx, mkKey(KeyZone, code), &z, "پهنه"); err != nil {
		return nil, err
	}
	return &z, nil
}

func (c *RegulationContract) ListZones(ctx contractapi.TransactionContextInterface) ([]Zone, error) {
	raw, err := queryByPrefix(ctx, KeyZone)
	if err != nil {
		return nil, err
	}
	out := []Zone{}
	for _, b := range raw {
		var z Zone
		if err := json.Unmarshal(b, &z); err == nil {
			out = append(out, z)
		}
	}
	return out, nil
}

// ---------------------------- تعرفه ----------------------------

// SetTariff ثبت دفترچه تعرفه یک سال. کل دفترچه به‌صورت JSON می‌آید چون
// تعداد ضرایبش زیاد است و ارسال تک‌تک آنها به‌عنوان آرگومان، امضای تابع را
// غیرقابل نگهداری می‌کند.
func (c *RegulationContract) SetTariff(ctx contractapi.TransactionContextInterface,
	year int64, tariffJSON string) error {

	if err := requireRole(ctx, RoleRegulator); err != nil {
		return err
	}
	var t Tariff
	if err := json.Unmarshal([]byte(tariffJSON), &t); err != nil {
		return fmt.Errorf("دفترچه تعرفه نامعتبر است: %v", err)
	}
	t.DocType = "tariff"
	t.Year = year
	t.UpdatedAt = txTime(ctx)

	// پیش‌فرض‌های ایمن — نبودشان یعنی تقسیم بر صفر یا فرمول بی‌معنا.
	if t.StandardSpanCm <= 0 {
		t.StandardSpanCm = 300
	}
	if t.StandardHeightCm <= 0 {
		t.StandardHeightCm = 400
	}
	if t.Article100MinPerMille <= 0 {
		t.Article100MinPerMille = 500 // نصف ارزش معاملاتی — کف قانونی
	}
	if t.Article100MaxPerMille <= 0 {
		t.Article100MaxPerMille = 3000 // سه برابر — سقف قانونی
	}
	if t.Article100MinPerMille > t.Article100MaxPerMille {
		return fmt.Errorf("کف جریمه ماده ۱۰۰ نمی‌تواند از سقف آن بیشتر باشد")
	}
	if t.InquirySlaDays <= 0 {
		t.InquirySlaDays = 10
	}
	if err := putJSON(ctx, mkKey(KeyTariff, fmt.Sprintf("%d", year)), &t); err != nil {
		return err
	}
	return emit(ctx, "TariffPublished", fmt.Sprintf("%d", year),
		fmt.Sprintf("دفترچه تعرفه سال %d منتشر شد", year), 0)
}

func (c *RegulationContract) GetTariff(ctx contractapi.TransactionContextInterface,
	year int64) (*Tariff, error) {
	var t Tariff
	if err := mustGetJSON(ctx, mkKey(KeyTariff, fmt.Sprintf("%d", year)), &t, "دفترچه تعرفه"); err != nil {
		return nil, err
	}
	return &t, nil
}

// ---------------------------- سقف انتشار تراکم ----------------------------

// SetQuota سقف انتشار توکن متراژ یک کاربری در یک منطقه.
//
// این مهم‌ترین اهرم سیاستی شبکه است. بدون سقف، توکن فقط یک واحد شمارش است.
// با سقف، تراکم یک دارایی کمیاب می‌شود: قیمت پیدا می‌کند، بازار دارد، و
// شهرداری می‌تواند به‌جای فروش بی‌حساب تراکم، ظرفیت را متناسب با زیرساخت
// عرضه کند. سقف را از ظرفیت واقعی (آب، فاضلاب، معبر، مدرسه) بگیرید، نه از
// بودجه سالانه — وگرنه همان چرخه‌ای تکرار می‌شود که بافت را اشباع کرد.
func (c *RegulationContract) SetQuota(ctx contractapi.TransactionContextInterface,
	region, use string, ceiling int64) error {

	if err := requireRole(ctx, RoleRegulator); err != nil {
		return err
	}
	if _, ok := UseTitleFa[use]; !ok {
		return fmt.Errorf("کاربری ناشناخته: «%s»", use)
	}
	if !MintableUse[use] {
		return fmt.Errorf("برای کاربری «%s» سقف انتشار معنا ندارد؛ این کاربری توکن منتشرشدنی ندارد",
			UseTitleFa[use])
	}
	var reg Region
	if err := mustGetJSON(ctx, mkKey(KeyRegion, region), &reg, "منطقه"); err != nil {
		return err
	}

	var q Quota
	ok, err := getJSON(ctx, quotaKey(region, use), &q)
	if err != nil {
		return err
	}
	if !ok {
		q = Quota{DocType: "quota", Region: region, Use: use}
	}
	// کاهش سقف به زیر مقدار منتشرشده مجاز نیست: توکن‌هایی که در دست مردم
	// است نمی‌شود با یک مصوبه باطل کرد.
	if ceiling < q.Minted {
		return fmt.Errorf("سقف جدید (%d) از میزان منتشرشده (%d) کمتر است؛ توکن‌های در گردش قابل ابطال نیستند",
			ceiling, q.Minted)
	}
	q.Ceiling = ceiling
	q.UpdatedAt = txTime(ctx)
	if err := putJSON(ctx, quotaKey(region, use), &q); err != nil {
		return err
	}
	return emit(ctx, "QuotaUpdated", quotaKey(region, use),
		fmt.Sprintf("سقف تراکم منطقه %s کاربری %s: %d متر مربع (منتشرشده %d)",
			region, UseTitleFa[use], ceiling, q.Minted), ceiling)
}

func (c *RegulationContract) GetQuota(ctx contractapi.TransactionContextInterface,
	region, use string) (*Quota, error) {
	var q Quota
	if err := mustGetJSON(ctx, quotaKey(region, use), &q, "سقف تراکم"); err != nil {
		return nil, err
	}
	return &q, nil
}

func (c *RegulationContract) ListQuotas(ctx contractapi.TransactionContextInterface) ([]Quota, error) {
	raw, err := queryByPrefix(ctx, KeyQuota)
	if err != nil {
		return nil, err
	}
	out := []Quota{}
	for _, b := range raw {
		var q Quota
		if err := json.Unmarshal(b, &q); err == nil {
			out = append(out, q)
		}
	}
	return out, nil
}

// ---------------------------- ارزیابی ضوابط ----------------------------

// ساختار ZoningVerdict در shared.go تعریف شده است.


// EvaluateZoning هسته «تصمیم‌گیری خودکار» شبکه است: یک درخواست را در برابر
// پهنه می‌سنجد و می‌گوید کجا منطبق است و کجا نه.
//
// این تابع قصداً query است و چیزی نمی‌نویسد، تا متقاضی بتواند قبل از تشکیل
// پرونده و پرداخت هزینه، هر تعداد سناریو را رایگان بیازماید. بخش بزرگی از
// رفت‌وبرگشت‌های شهرداری همین است: نقشه‌ای که چهار بار برمی‌گردد چون کسی از
// اول نگفته سقف طبقات چند است.
func (c *RegulationContract) EvaluateZoning(ctx contractapi.TransactionContextInterface,
	zoneCode string, landArea int64, floorsJSON string) (*ZoningVerdict, error) {

	var z Zone
	if err := mustGetJSON(ctx, mkKey(KeyZone, zoneCode), &z, "پهنه"); err != nil {
		return nil, err
	}
	var floors []FloorSpec
	if err := json.Unmarshal([]byte(floorsJSON), &floors); err != nil {
		return nil, fmt.Errorf("مشخصات طبقات نامعتبر است: %v", err)
	}

	v := &ZoningVerdict{
		AllowedArea:   perMille(landArea, z.FarPerMille),
		MaxFloors:     z.MaxFloors,
		NeedsHeritage: z.HeritageBuffer,
		Violations:    []string{},
		Notes:         []string{},
	}

	if z.MinLotArea > 0 && landArea < z.MinLotArea {
		v.Violations = append(v.Violations, fmt.Sprintf(
			"مساحت عرصه (%d م²) از حداقل قابل ساخت این پهنه (%d م²) کمتر است",
			landArea, z.MinLotArea))
	}

	allowedUse := map[string]bool{}
	for _, u := range z.AllowedUses {
		allowedUse[u] = true
	}

	var above, ground int64
	for _, f := range floors {
		v.RequestedArea += f.Area
		if f.Level > 0 {
			above++
		}
		if f.Level == 0 {
			ground += f.Area
		}
		if !allowedUse[f.Use] {
			v.Violations = append(v.Violations, fmt.Sprintf(
				"کاربری «%s» در طبقه %d در این پهنه مجاز نیست", UseTitleFa[f.Use], f.Level))
		}
	}
	v.UsedFloors = above

	if z.MaxFloors > 0 && above > z.MaxFloors {
		v.Violations = append(v.Violations, fmt.Sprintf(
			"تعداد طبقات (%d) از سقف پهنه (%d) بیشتر است", above, z.MaxFloors))
	}
	maxGround := perMille(landArea, z.CoveragePerMille)
	if ground > maxGround {
		v.Violations = append(v.Violations, fmt.Sprintf(
			"سطح اشغال همکف (%d م²) از حد مجاز (%d م²) بیشتر است", ground, maxGround))
	}
	if v.RequestedArea > v.AllowedArea {
		v.ExcessArea = v.RequestedArea - v.AllowedArea
		// مازاد به‌خودی‌خود تخلف نیست: یا با توکن انتقالی پوشش داده می‌شود یا
		// به کمیسیون ماده ۵ می‌رود. اگر پهنه گیرنده نباشد، هیچ‌کدام ممکن نیست.
		if z.TdrReceiving {
			maxTdr := perMille(v.AllowedArea, z.TdrMaxPerMille)
			if v.ExcessArea > maxTdr {
				v.Violations = append(v.Violations, fmt.Sprintf(
					"مازاد درخواستی (%d م²) از سقف تراکم انتقالی این پهنه (%d م²) بیشتر است",
					v.ExcessArea, maxTdr))
			} else {
				v.Notes = append(v.Notes, fmt.Sprintf(
					"مازاد %d م² با خرید توکن حق توسعه از بازار قابل تأمین است", v.ExcessArea))
			}
		} else {
			v.Notes = append(v.Notes, fmt.Sprintf(
				"مازاد %d م² نیازمند تصویب کمیسیون ماده ۵ است (این پهنه گیرنده تراکم انتقالی نیست)",
				v.ExcessArea))
		}
	}
	if z.HeritageBuffer {
		v.Notes = append(v.Notes, "پلاک در حریم آثار تاریخی است؛ استعلام میراث فرهنگی اجباری است")
	}
	if z.FaultBuffer {
		v.Notes = append(v.Notes, "پلاک در حریم گسل است؛ سقف طبقات کاهش‌یافته اعمال شده است")
	}
	v.Compliant = len(v.Violations) == 0
	return v, nil
}

func splitCSV(s string) []string {
	out := []string{}
	cur := ""
	for _, r := range s {
		if r == ',' || r == '،' || r == ' ' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func main() {
	cc, err := contractapi.NewChaincode(&RegulationContract{})
	if err != nil {
		log.Panicf("خطا در ساخت قرارداد Regulation: %v", err)
	}
	if err := cc.Start(); err != nil {
		log.Panicf("خطا در اجرای قرارداد Regulation: %v", err)
	}
}
