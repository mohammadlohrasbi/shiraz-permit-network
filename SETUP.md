# راه‌اندازی از صفر تا صد

از یک سرور خالی اوبونتو تا شبکه‌ای که پروانه صادر می‌کند.

هر گام یک **معیار موفقیت** دارد. اگر آن را ندیدید، جلو نروید — در فابریک خطای یک گام معمولاً سه گام بعد و با پیام نامربوط ظاهر می‌شود.

---

## پیش از شروع

| نیاز | حداقل | بررسی |
|---|---|---|
| اوبونتو | ۲۰.۰۴ | `lsb_release -d` |
| RAM آزاد | ۲ گیگابایت (برای ۳ نود Raft) | `free -m` |
| دیسک آزاد | ۳۰ گیگابایت | `df -h /` |
| دسترسی اینترنت | برای تصاویر Docker و وابستگی‌های Go | — |
| کاربر | `root` یا `sudo` | — |

فرمول حافظه: `۱۲۰۰ + ۲۰۰ × تعداد نود` مگابایت. برای ۳ نود یعنی ۱۸۰۰ مگابایت آزاد.

---

## گام ۱ — نصب پیش‌نیازها

```bash
sudo apt update
sudo apt install -y git curl openssl ca-certificates
```

### Docker با compose نسخه ۲

```bash
curl -fsSL https://get.docker.com | sudo sh
sudo systemctl enable --now docker
```

**معیار موفقیت:**

```bash
docker compose version     # باید v2.x بدهد
```

اگر `docker-compose version` (با خط تیره) کار می‌کند ولی `docker compose` نه، نسخه ۱ دارید و **ناسازگار است**. بسته `docker-compose-plugin` را نصب کنید.

### Go

```bash
sudo apt install -y golang-go
```

**معیار موفقیت:** `go version` → ۱.۱۸ یا بالاتر.

### Node.js

```bash
curl -fsSL https://deb.nodesource.com/setup_20.x | sudo -E bash -
sudo apt install -y nodejs
```

**معیار موفقیت:** `node -v` → ۱۸ یا بالاتر.

### تصاویر فابریک

```bash
docker pull hyperledger/fabric-peer:2.5
docker pull hyperledger/fabric-orderer:2.5
docker pull hyperledger/fabric-ca:1.5
docker pull hyperledger/fabric-tools:2.5
```

**معیار موفقیت:** `docker images | grep fabric` → چهار سطر.

از قبل کشیدن این تصاویر یک انتخاب عمدی است: اگر وسط `bootstrap` دانلود شوند و شبکه کند باشد، تایم‌اوت‌ها خطاهایی می‌سازند که به‌نظر مشکل پیکربندی می‌آیند.

---

## گام ۲ — گرفتن کد

```bash
cd /root
git clone https://github.com/mohammadlohrasbi/shiraz-permit-network.git
cd shiraz-permit-network
```

مسیر `/root/shiraz-permit-network` پیش‌فرض همه اسکریپت‌هاست. اگر جای دیگری می‌گذارید، در هر دستور `ROOT_DIR=/مسیر/شما` بگذارید.

### مجوز اجرا

```bash
find . -name '*.sh' -not -path './.git/*' -exec chmod +x {} +
chmod +x scripts/builders/golang/bin/*
```

برای اینکه در `clone` بعدی تکرار نشود، در خود Git هم ثبتش کنید:

```bash
git update-index --chmod=+x $(find . -name '*.sh' -not -path './.git/*') scripts/builders/golang/bin/*
git commit -m "ثبت بیت اجرا برای اسکریپت‌ها" && git push
```

**معیار موفقیت:** `ls -l install.sh` → با `x` شروع شود (`-rwxr-xr-x`).

> **`install.sh` را لازم ندارید.** آن اسکریپت برای وقتی است که بسته‌ای دانلودشده را روی مخزن موجود بریزید. وقتی مستقیم `clone` می‌کنید، کد سرجایش است.
>
> اگر `sudo: ./install.sh: command not found` دیدید، مشکل نبودن فایل نیست — نبودن بیت اجراست. `sudo` در این حالت همین پیام گمراه‌کننده را می‌دهد.

---

## گام ۳ — ساخت و کامپایل قراردادها

```bash
cd /root/shiraz-permit-network/scripts
./build-chaincode.sh
```

این اسکریپت `shared.go` را در پوشه هر قرارداد کپی می‌کند (هر قرارداد یک ماژول Go مستقل است)، وابستگی‌ها را می‌گیرد، بررسی ساختاری می‌کند، و **واقعاً کامپایل می‌کند**.

**معیار موفقیت:**

```
✅ همه بررسی‌های ساختاری پاس شد
موفق: هر 10 قرارداد کامپایل شدند و آماده deploy هستند
```

**این گام را قبل از بالا آوردن شبکه انجام دهید.** اگر کامپایل بشکند همین‌جا می‌ایستید، به‌جای اینکه ده دقیقه بعد ببینید `0/10 قرارداد commit شده` و علتش زیر چند صفحه لاگ دفن باشد.

اگر خطای `proxy.golang.org` گرفتید، دسترسی به پروکسی Go ندارید:

```bash
go env -w GOPROXY=https://goproxy.io,direct    # یا هر آینه در دسترس
./build-chaincode.sh
```

---

## گام ۴ — بالا آوردن شبکه

```bash
NODES=3 ./bootstrap-secure.sh
```

پیش از هر کاری ببینید چه می‌کند:

```bash
DRY_RUN=1 NODES=3 ./bootstrap-secure.sh
```

این ترتیب را اجرا می‌کند و ترتیب قابل جابه‌جایی نیست:

```
بررسی پیش‌نیاز → fix-paths → network.sh (CA و MSP و گواهی)
  → setup-raft.sh (نودهای اضافی و configtx) → set-tls.sh (صدور TLS)
  → بلوک پیدایش → کانتینرها → کانال‌ها → استقرار قراردادها → بذرکاری
```

سه دلیل که ترتیب حیاتی است:

1. `setup-raft` پوشه نودهای جدید را می‌سازد و `set-tls` باید **بعد** بیاید، وگرنه نودهای جدید گواهی ندارند و Raft که با pinning کار می‌کند ردشان می‌کند.
2. بلوک پیدایش باید **آخر** ساخته شود، چون هم نوع اجماع و هم مسیر گواهی consenter ها در آن است.
3. `network.sh` فایل `builders/golang/bin/run` را از heredoc خودش بازمی‌نویسد. اصلاح دستی آن فایل بی‌فایده است — باید در heredoc باشد (که هست).

گزینه‌ها:

```bash
NODES=1 ./bootstrap-secure.sh                  # solo، برای سرور کم‌حافظه
NODES=5 ./bootstrap-secure.sh                  # خوشه پنج‌نودی
CHANNELS="permitchannel" ./bootstrap-secure.sh # فقط کانال هسته
```

**معیارهای موفقیت — هر سه را ببینید:**

```bash
# ۱) انتخاب رهبر Raft
docker logs orderer.example.com 2>&1 | grep -i "elected leader"
#    باید چیزی شبیه: raft.node: 2 elected leader 1 at term 2

# ۲) همه کانتینرها بالا
docker ps --format '{{.Names}}\t{{.Status}}' | wc -l
#    ۳ نود Raft → ۱۳ سرویس

# ۳) قراردادها commit شده
./deploy-staged.sh list
#    باید بدهد: permitchannel 9/9   و   auditchannel 1/1
```

اگر `elected leader` نیامد، سراغ بخش عیب‌یابی `RUNBOOK.md` بروید — سه علت رایج آنجا با فرمان تشخیص آمده.

---

## گام ۵ — بذرکاری

شبکه بالاست ولی خالی. بدون منطقه پهنه‌ای ثبت نمی‌شود، بدون پهنه پلاکی معنا ندارد، بدون تعرفه موتور عوارض تقسیم بر صفر می‌کند.

```bash
./seed-network.sh
```

اگر `bootstrap` خودش این را اجرا کرده باشد، اجرای دوباره بی‌ضرر است (رکوردها بازنویسی می‌شوند).

**معیار موفقیت:**

```
ListRegions    11 رکورد
ListZones      30 رکورد
ListQuotas     38 رکورد
موفق: بذرکاری تمام شد
```

قبل از نوشتن ببینید چه فرستاده می‌شود:

```bash
VERIFY_ONLY=1 ./seed-network.sh
```

---

## گام ۶ — داشبورد

```bash
cd /root/shiraz-permit-network/server
npm install
node index.js
```

**معیار موفقیت:** `http://<آی‌پی سرور>:3000` باز شود و در سربرگ «برخط» و «۸ سازمان» ببینید، و شبکه ظرفیت مناطق پر شود.

اگر شبکه ظرفیت خالی بود و نوشت «دفتر خالی است»، گام ۵ انجام نشده.

### سرویس دائمی

```bash
sudo tee /etc/systemd/system/dashboard.service >/dev/null <<'EOF'
[Unit]
Description=Shiraz Permit Network Dashboard
After=docker.service

[Service]
WorkingDirectory=/root/shiraz-permit-network/server
ExecStart=/usr/bin/node index.js
Restart=always
Environment=NODE_ENV=production

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload && sudo systemctl enable --now dashboard
```

### اگر داشبورد از بیرون در دسترس است

```bash
cd ../scripts && ./secure-dashboard.sh && ./harden-docker-ports.sh
```

بدون این، پورت‌های peer روی تمام رابط‌ها باز می‌مانند.

---

## گام ۷ — آزمون پذیرش

اینجا واقعاً می‌فهمید شبکه کار می‌کند. هر فراخوانی با هویت یک سازمان امضا می‌شود.

```bash
cd /root/shiraz-permit-network/scripts
```

### الف) خواندن مقررات

```bash
docker exec -e CORE_PEER_LOCALMSPID=org1MSP \
  -e CORE_PEER_MSPCONFIGPATH=/etc/hyperledger/fabric/admin-msp \
  -e CORE_PEER_ADDRESS=peer0.org1.example.com:7051 \
  peer0.org1.example.com peer chaincode query \
  -C permitchannel -n Regulation \
  -c '{"function":"GetZone","Args":["R510"]}'
```

انتظار: پهنه مسکونی معالی‌آباد با تراکم ۳۰۰۰ در هزار و سطح اشغال ۶۰۰ در هزار.

### ب) ارزیابی ضوابط — قلب منطق

```bash
docker exec -e CORE_PEER_LOCALMSPID=org1MSP \
  -e CORE_PEER_MSPCONFIGPATH=/etc/hyperledger/fabric/admin-msp \
  -e CORE_PEER_ADDRESS=peer0.org1.example.com:7051 \
  peer0.org1.example.com peer chaincode query \
  -C permitchannel -n Regulation \
  -c '{"function":"EvaluateZoning","Args":["R510","RES","300","900","6","4"]}'
```

زمین ۳۰۰ متری در پهنه‌ای با تراکم ۳۰۰٪ ⇒ مجاز ۹۰۰ متر. درخواست ۹۰۰ متر ⇒ `compliant: true` و `excessArea: 0`.

حالا همان را با ۱۲۰۰ متر بخواهید: باید `excessArea: 300` بدهد و پرونده‌ای با این متراژ مجبور می‌شود از کمیسیون ماده ۵ عبور کند.

### ج) آزمون کنترل دسترسی — مهم‌ترین آزمون

سازمانی که نقشش را ندارد باید **رد شود**:

```bash
docker exec -e CORE_PEER_LOCALMSPID=org4MSP \
  -e CORE_PEER_MSPCONFIGPATH=/etc/hyperledger/fabric/admin-msp \
  -e CORE_PEER_ADDRESS=peer0.org4.example.com:10051 \
  peer0.org4.example.com peer chaincode invoke \
  -o orderer.example.com:7050 -C permitchannel -n Regulation \
  -c '{"function":"SetQuota","Args":["1","RES","999999999"]}'
```

**اینجا موفقیت یعنی خطا.** مالک (`org4` با نقش `CITIZEN`) نباید بتواند سقف تراکم منطقه را عوض کند. اگر این دستور `status:200` داد، کنترل دسترسی کار نمی‌کند و نباید جلو بروید.

---

## کارهای دوره‌ای

```bash
# پس از reboot
cd /root/shiraz-permit-network/config
docker compose up -d
docker compose -f docker-compose-root-ca.yml up -d
sudo systemctl start dashboard

# تعرفه سال جدید — قرارداد دست نمی‌خورد
cd ../scripts && YEAR=1406 ./seed-network.sh tariff

# روشن/خاموش کردن TLS — بدون بازسازی شبکه
./set-tls.sh off
cd ../config && docker compose down && docker compose up -d
```

## بازسازی کامل

```bash
cd /root/shiraz-permit-network/config
docker compose down -v
docker volume prune -f
cd ../scripts
./build-chaincode.sh && NODES=3 ./bootstrap-secure.sh && ./seed-network.sh
```

`-v` را حذف نکنید. بدون آن بلوک پیدایش قدیمی در volume می‌ماند، اوردرر می‌گوید «Not bootstrapping the system channel because of existing channels» و شبکه با پیکربندی قبلی بالا می‌آید — بدترین نوع خطا، چون سالم به نظر می‌رسد.

---

## اگر جایی گیر کردید

بخش عیب‌یابی [`RUNBOOK.md`](RUNBOOK.md) هفت خطای واقعی را با فرمان تشخیص دارد: `chaincode registration failed`، `tls: bad certificate` بین نودهای Raft، بلوک پیدایش کهنه، تعارض MVCC، کمبود حافظه هنگام استقرار، و دو مورد دیگر.

هنگام گزارش مشکل این سه را بفرستید — با کمترشان تشخیص حدس می‌شود:

```bash
./deploy-staged.sh list
docker ps -a --format '{{.Names}}\t{{.Status}}'
docker logs orderer.example.com 2>&1 | tail -40
```

---

## یادآوری پیش از استفاده واقعی

اعداد `seed-network.sh` نمونه ساختاری‌اند، نه رونوشت اسناد رسمی. پیش از هر بهره‌برداری واقعی چهار چیز باید جایگزین شود:

- **قیمت‌های منطقه‌ای** ← دفترچه ارزش معاملاتی املاک شیراز (ستون‌های `P` و `BP`)
- **ضرایب تعرفه** ← مصوبه جاری شورای اسلامی شهر شیراز
- **کدها و مرزهای پهنه** ← فایل GIS طرح تفصیلی مصوب کمیسیون ماده ۵
- **سقف انتشار توکن** ← مطالعه ظرفیت زیرساخت (آب، فاضلاب، معبر، مدرسه)

هر چهار مورد فقط ویرایش `seed-network.sh` و اجرای دوباره‌اش است. قرارداد دست نمی‌خورد، چون تعرفه و پهنه داده‌اند نه کد.
