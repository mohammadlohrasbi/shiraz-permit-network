# راهنمای راه‌اندازی

## پیش‌نیازها

| مورد | حداقل | چرا |
|---|---|---|
| Ubuntu | ۲۰.۰۴ به بالا | — |
| RAM | ۴ گیگابایت | ۸ peer + ۳ orderer + ۲ CA. با ۳٫۷ گیگ کار می‌کند ولی dev-container باید بین کانال‌ها پاک شود |
| دیسک | ۳۰ گیگابایت | تصاویر فابریک و ledger |
| Docker | با `docker compose` نسخه ۲ | `docker-compose` نسخه ۱ ناسازگار است |
| Go | ۱.۱۸ به بالا | کامپایل chaincode |
| Node.js | ۱۸ به بالا | سرور و ابزار |
| OpenSSL | — | تولید گواهی |

```bash
docker compose version   # باید v2 باشد، نه v1
go version
node -v
free -h
```

---

## مسیر استاندارد

### گام ۰ — نصب

اگر پروژه را با `git clone` گرفته‌اید، **اول مجوز اجرا را درست کنید**:

```bash
cd shiraz-permit-network
bash fix-permissions.sh --git
```

بیت اجرا خصوصیتی است که Git فقط در صورت ثبت در ایندکس نگه می‌دارد. اگر فایل‌ها یک بار بدون آن commit شده باشند، هر clone تازه ۶۴۴ می‌گیرد. نشانه‌اش دو پیام متفاوت است که هر دو یک علت دارند:

```
-bash: ./build-chaincode.sh: Permission denied      ← پیام گویا
sudo: ./install.sh: command not found               ← پیام گمراه‌کننده
```

دومی به‌خصوص فریبنده است: به نظر می‌رسد فایل نیست، در حالی که `ls` نشانش می‌دهد. `sudo` وقتی فایل قابل اجرا نباشد همین را می‌گوید.

سپس:

```bash
sudo ./install.sh
cd /root/shiraz-permit-network/scripts
```

`install.sh` بازگشتی کپی می‌کند و فایل‌های نقطه‌دار را هم می‌گیرد. هر دو نکته از باگ واقعی پروژه قبل آمده‌اند: `*` فایل `.env` را نمی‌گرفت و `[ -f "$f" ] || continue` زیرپوشه‌ها را رد می‌کرد، پس `builders/golang/bin/run` هرگز کپی نمی‌شد.

### گام ۱ — کامپایل قراردادها

```bash
./build-chaincode.sh
```

**این را قبل از بالا آوردن شبکه اجرا کنید.** اگر کامپایل شکست بخورد، اسکریپت `exit 1` می‌دهد و همان‌جا متوقف می‌شوید — به‌جای اینکه ده دقیقه بعد ببینید `0/10 قرارداد commit شده` و علتش زیر چند صفحه لاگ دفن است.

چون همه قراردادها یک `shared.go` دارند، اگر یکی کامپایل شود بقیه هم می‌شوند.

### گام ۲ — بالا آوردن شبکه

```bash
NODES=3 ./bootstrap-secure.sh
```

`bootstrap-secure.sh` ترتیب را رعایت می‌کند و ترتیب اینجا حیاتی است:

```
fix-paths → network.sh → setup-raft.sh → set-tls.sh → قراردادها
   → بلوک پیدایش → کانتینرها → کانال‌ها → استقرار
```

سه دلیل که این ترتیب قابل جابه‌جایی نیست:

1. `setup-raft.sh` پوشه نودهای جدید را می‌سازد و `set-tls.sh` باید **بعد** بیاید، وگرنه نودهای جدید گواهی ندارند و Raft که با pinning کار می‌کند ردشان می‌کند.
2. بلوک پیدایش باید **آخر** ساخته شود، چون هم نوع orderer و هم مسیر گواهی consenter ها در آن است.
3. `network.sh` فایل `builders/golang/bin/run` را از heredoc خودش بازمی‌نویسد. هر اصلاح دستی روی آن فایل با اجرای دوباره `network.sh` پاک می‌شود — اصلاح باید در خود heredoc باشد.

گزینه‌ها:

```bash
DRY_RUN=1 NODES=3 ./bootstrap-secure.sh   # فقط نمایش کارها
NODES=1 ./bootstrap-secure.sh             # solo، برای سرور کم‌حافظه
NODES=5 ./bootstrap-secure.sh             # خوشه پنج‌نودی
```

### گام ۳ — بذرکاری

```bash
./seed-network.sh
```

بدون این، شبکه بالاست ولی هیچ کاری نمی‌کند: بدون منطقه پهنه‌ای ثبت نمی‌شود، بدون پهنه پلاکی معنا ندارد، بدون تعرفه موتور عوارض تقسیم بر صفر می‌کند.

```bash
VERIFY_ONLY=1 ./seed-network.sh   # ببینید چه فرستاده می‌شود، بدون نوشتن
./seed-network.sh tariff          # فقط تعرفه — برای سال جدید
YEAR=1406 ./seed-network.sh tariff
```

### گام ۴ — داشبورد

```bash
cd ../server && npm install && node index.js
```

روی `http://<سرور>:3000`. اگر به بیرون در دسترس است، اول `./secure-dashboard.sh` را اجرا کنید.

### گام ۵ — بررسی سلامت

```bash
./deploy-staged.sh list        # باید permitchannel 9/9 و auditchannel 1/1 بدهد
docker ps --format '{{.Names}}\t{{.Status}}'
docker logs orderer.example.com 2>&1 | grep -i "elected leader"
```

---

## عیب‌یابی

این بخش از شکست‌های واقعی پروژه قبل ساخته شده. هر مورد یک بار یک روز گرفت.

### هیچ قراردادی commit نمی‌شود ولی deploy «موفق» می‌گوید

خطای کامپایل Go که بی‌صدا رد شده. `./build-chaincode.sh` را جدا اجرا کنید و خروجی کامل را ببینید.

### `chaincode registration failed: container exited with 0`

معمولاً TLS در external builder. فایل `scripts/builders/golang/bin/run` باید `CORE_PEER_TLS_ENABLED` را از `chaincode.json` بخواند.

دو دام در همان فایل:

- **این اسکریپت داخل کانتینر peer اجرا می‌شود، نه روی هاست.** تصویر `fabric-peer` نه `python3` دارد نه `jq`. استفاده از آنها `exit status 127` می‌دهد که peer آن را «builder run failed» گزارش می‌کند. فقط `sed`.
- **shim دو صورت متغیر دارد**: `*_PATH` فایل PEM کدشده با base64 می‌خواهد، `*_FILE` فایل PEM خام. آنچه از `chaincode.json` می‌آید خام است ⇒ `_FILE`.

### `tls: bad certificate` بین نودهای Raft

هویت اوردررهای دوم به بعد در `fabric-ca-server-config.yaml` ثبت نشده و enroll با «Failed to get user» رد شده، بعد به گواهی خودامضا افتاده ⇒ سه ریشه متفاوت.

```bash
docker logs rca-main 2>&1 | grep -i "no rows"
openssl x509 -noout -issuer -in <مسیر گواهی>   # همه باید rca-main باشند
```

### orderer با «consensus type: solo» بالا می‌آید هرچند configtx می‌گوید etcdraft

بلوک پیدایش قدیمی است. دو علت ممکن:

```bash
configtxgen -inspectBlock <بلوک> | grep -ci etcdraft   # باید صفر نباشد
docker compose down -v                                  # بدون -v بلوک قدیمی در volume می‌ماند
```

بدون `-v`، اوردرر می‌گوید «Not bootstrapping the system channel because of existing channels» و بلوک تازه را نادیده می‌گیرد.

### Caliper «۰ موفق از ۵۰۰» بدون هیچ خطای گواهی

ابزار پیکربندی بدون TLS ساخته. `config.js` متغیر `CORE_PEER_TLS_ENABLED` را از محیط سرویس داشبورد می‌خواند و وقتی اسکریپت را دستی از خط فرمان می‌زنید ست نیست.

```bash
./patch-tls-detect.sh    # قبل از gen-caliper-network.js و fix-tape-policy.sh
```

### `number of peer addresses (8) does not match the number of TLS root cert files (1)`

هر `--peerAddresses` یک `--tlsRootCertFiles` می‌خواهد.

### تراکنش با `ENDORSEMENT_POLICY_FAILURE` رد می‌شود ولی سیاست درست است

نشانه کلاسیک عدم قطعیت — دو peer دو نتیجه ساخته‌اند.

```bash
node scripts/check-go.js
```

سه مظنون به ترتیب احتمال: `time.Now()`، محاسبه اعشاری، پیمایش map بدون `sort.Strings`. مورد سوم موذی‌ترین است چون حتی ساخت **پیام خطا** از روی map کافی است.

### تراکنش‌ها با `MVCC_READ_CONFLICT` رد می‌شوند

کلید داغ: چند تراکنش هم‌زمان یک رکورد را می‌خوانند و بازمی‌نویسند. اینجا نامزد اصلی رکورد `QUOTA~<منطقه>~<کاربری>` است — هر صدور پروانه در یک منطقه همان رکورد را به‌روز می‌کند.

زیر بار سنگین این خودش را نشان می‌دهد. راه‌حل استاندارد شکستن شمارنده به چند stripe است. **قبل از پیاده‌سازی، اول اندازه بگیرید**: در نرخ واقعی صدور پروانه یک شهرداری (چند ده مورد در روز، نه چند صد در ثانیه) این احتمالاً هرگز مشکل نمی‌شود، و بهینه‌سازی زودهنگام پیچیدگی بی‌فایده اضافه می‌کند.

### کمبود حافظه هنگام استقرار

```bash
./deploy-staged.sh cleanup-dev permitchannel
free -h
```

هر chaincode یک dev-container می‌سازد. با ۳٫۷ گیگ، ده تا هم‌زمان OOM می‌دهد — و OOM killer معمولاً peer را می‌برد نه دستور را، پس خطایی می‌بینید که به استقرار ربطی ندارد.

---

## کارهای دوره‌ای

```bash
# پس از reboot
cd /root/shiraz-permit-network/config
docker compose up -d
docker compose -f docker-compose-root-ca.yml up -d
systemctl start dashboard

# روشن یا خاموش کردن TLS — یک خط در .env، بدون بازسازی شبکه
./set-tls.sh on   &&  cd ../config && docker compose down && docker compose up -d

# تعرفه سال جدید — بدون لمس قرارداد
YEAR=1406 ./seed-network.sh tariff
```

## بازسازی کامل

```bash
cd /root/shiraz-permit-network/config
docker compose down -v
docker volume prune -f
cd ../scripts && NODES=3 ./bootstrap-secure.sh && ./seed-network.sh
```

`-v` را حذف نکنید. بدون آن بلوک پیدایش قدیمی می‌ماند و شبکه با پیکربندی قبلی بالا می‌آید — که بدترین نوع خطاست، چون شبکه سالم به نظر می‌رسد.
