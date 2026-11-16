# Yayın öncesi doğrulama — 15 Kasım 2026

> Bu rapor 14–15 Kasım kapsamını korur. Sonraki canlı dil testleri,
> dayanıklılık kontrolleri ve paket düzeltmeleri için
> [16 Kasım sonuçlarına](release-completion.md) bakın.

14–15 Kasım kontrolleri, `1d8ca10` üzerine bu raporla birlikte gelen kurulum,
dokümantasyon, regresyon testi ve zlib güncellemelerini kapsar. Veritabanları,
hesap kimlikleri, OAuth yanıtları ve anahtarlar sentetiktir. Mevcut müşteri
veritabanı veya gerçek Slack hesabında işlem yapılmadı.

## Sonuçlar

| Kontrol | Sonuç ve kapsam |
| --- | --- |
| Go testleri | `go test -race -count=1 -coverprofile=… -json ./...` geçti. 327 ana test, alt testlerle birlikte 901 başarılı test kaydı; 2 canlı/ücretli model testi bilinçli olarak kapalı. Test sonucu önbelleği kullanılmadı. |
| Statik kontrol | `go vet ./...`, Staticcheck 0.7.0 ve `git diff --check` geçti. |
| Dashboard JavaScript | 10 test geçti: logout, geç gelen yanıtlar, XSS kaçışı, filtreler ve kayıt durumu. |
| Kurulum yapılandırması | 5 test geçti: bağımsız 32-byte anahtarlar, 0600 dosya izni, mevcut `.env` dosyasını/symlink hedefini koruma, harici PostgreSQL ve özel host portları. |
| PostgreSQL | Gerçek PostgreSQL 17 üzerinde repository ve yeni şirket kurulum testleri geçti. Boş veritabanına 15 migration uygulandı. |
| Şirket kurulum akışı | Gerçek HTTP sunucusu/cookie jar ve üretim PostgreSQL repository'leriyle iki ayrı şirket kuruldu. Yalnız Slack'in uzaktaki OAuth kod değişimi taklit edildi. |
| Yerel Docker kurulumu | Temiz kaynak kopyasında `setup-env.py` ve Compose ile veritabanı + uygulama sağlıklı açıldı. Non-root kullanıcı, yazılabilir kalıcı durum ve init süreci doğrulandı. |
| Harici veritabanı | Ayrı Compose projesinde yalnız uygulama açıldı; mevcut test PostgreSQL'ine bağlandı, migration ve dashboard kontrolleri geçti. |
| Yeniden başlatma | Test verisi korundu, migration sayısı 15 kaldı ve servisler tekrar sağlıklı oldu. |
| HTTP kontrolleri | 19 kontrol geçti: sağlık, oturumsuz panel, no-store/güvenlik başlıkları, eski API'lerin 410 dönmesi, `.env` erişiminin reddi, metrics token denetimi, eksik Slack yapılandırması, geçersiz/eski/gelecek imzalar ve gövde boyutu sınırı. |
| Hatalı yapılandırma | Eksik imzalama anahtarı ve localhost dışındaki HTTP origin ile sunucu açılışı reddedildi. |
| Codex | Pinlenmiş 0.164.0 ile araç çağrısı, model devri ve eksik rollout kurtarma testleri geçti. zlib güncellemesinden sonra Linux runtime içinde tekrar doğrulandı. |
| Go bağımlılık güvenliği | govulncheck 1.9.0: `No vulnerabilities found.` |
| Gizli bilgi taraması | Gitleaks 8.30.1 ile 119 commit ve yayımlanacak kaynak kopyası temiz. Kurulumda oluşturulan sentetik `.env` dosyası yayımlanacak kaynaklara dahil değil. |
| İmaj güvenliği | Trivy 0.76.0 ile yamalı Linux arm64 imajında, tüm önem seviyelerinde 0 bulgu. Ignore listesi veya `--ignore-unfixed` kullanılmadı. Tespit edilen hedefler Alpine 3.24.4 paketleri, Node paketleri ve Go binary'si. |
| Eşzamanlı HTTP | Yerel `/health/db` üzerinde 10 paralel bağlantıyla 100 isteğin tamamı 200 döndü. Bu bir AI yük/kapasite testi değildir. |

Go paketlerinin varsayılan ölçüm biçimiyle toplam statement coverage %65,0.
Paketler arası çağrılar için `-coverpkg=./...` kullanılmadığından bu sayı tüm
entegrasyon kapsamını göstermez; özellikle migration/server başlangıcı ayrıca
gerçek veritabanı ve konteyner kontrolleriyle sınandı. Kapsam %100 değildir.

Son yerel taramanın imaj kimliği:
`sha256:6b6f99ce4e95a9399ea2366dcec164acaea8d3ac87c10852a7b4f8fcce97c924`.

## Bulunan ve düzeltilenler

1. **Orta seviyeli imaj bulgusu:** İlk tüm-seviye taramada `zlib 1.3.2-r1` için
   CVE-2026-4738 bulundu. Runtime kurulumu en az `1.4-r0` gerektirecek şekilde
   güncellendi. Yamalı sürüm [Alpine güvenlik kaydında](https://security.alpinelinux.org/vuln/CVE-2026-4738)
   doğrulandı; yeniden tarama temiz çıktı.
2. **Port çakışması:** Gerçek temiz kurulum, host üzerindeki mevcut PostgreSQL
   nedeniyle 5432'ye bağlanamadı. `NUDGY_POSTGRES_PORT` ve `NUDGY_HTTP_PORT`
   eklendi. Özel portlarla kurulum ve veritabanına içeriden erişim doğrulandı.
3. **Harici PostgreSQL kurulumu:** Varsayılan Compose'un `DATABASE_URL` değerini
   değiştirmesi, sadece `.env` düzenleyen operatörü yanıltabiliyordu. Bağlantıyı
   koruyan ve yerel PostgreSQL başlatmayan `docker-compose.external.yml` eklendi.
4. **Eski güvenlik belgeleri:** Şifreli saklanan Slack/Raindrop tokenlarının düz
   metin olduğu ve kalıcı OAuth state'in bellekte tutulduğu yönündeki güncelliğini
   yitirmiş ifadeler düzeltildi. Eski inceleme raporu tarihsel olarak işaretlendi.

Yeni `TestCompanyInstallationBrowserAndPostgres`, kurulumun gerçek cookie ve
veritabanı sınırlarını birlikte doğrular: eksik browser cookie, tek kullanımlık
OAuth state, şifreli bot tokenı, kurucunun yönetici rolü, query parametresiyle
başka şirkete geçememe ve yalnız hedef şirketin oturumlarının iptali.
Kurulum yapılandırma testleri CI'ın container işine eklendi.

## Canlı kabul testinin sınırları

Bu kontroller bağımsız penetrasyon testi veya hatasızlık garantisi değildir.
Başka bir şirketin gerçek Slack workspace'inde uygulama onayı, OAuth, kanal
davetleri, Slack'in olay teslimi ve gerçek sağlayıcı hesabıyla tam tur bu
incelemede yapılmadı. Ücretli model kalitesi, gerçek API kota/erişimleri,
GitHub/GitLab/Jira/Trello kurumsal politikaları ve üretim ölçeğindeki yük ayrıca
doğrulanmalı. Harici servis hatalarını bu test sonuçları ortadan kaldırmaz.

Yayın mesajında “açık kaynak, kendi sunucunda çalıştırılabilir; otomatik testler ve
güvenlik taramaları geçti” denebilir. “Her şirkette hatasız çalışır” veya
“bütün güvenlik açıklarından arındırıldı” sonucu çıkarılamaz.

Ham yerel sonuçlar `.local/release-audit/` altında tutulur ve Git'e eklenmez:
`go-tests.jsonl`, `coverage.out`, `company-test.log`, `installation-tests.log`,
`dashboard-tests.log`, `lint.log`, `govulncheck.log`, `gitleaks-history.log`,
`gitleaks-publishable.log`, `http-smoke.json`, `concurrent-http.json`,
`trivy-image.json`, `trivy-patched.json`, `native-patched.log` ve kurulum logları.
