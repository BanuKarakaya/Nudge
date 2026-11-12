# Açık kaynak hazırlık incelemesi

Tarih: **12 Kasım 2026**. Başlangıç sürümü `af4d76a`; inceleme sırasında mevcut
panel çalışması ayrı bir işlem tarafından `edaae22` olarak kaydedildi. Bu inceleme
panel değişikliklerini geri almaz. Son doğrulamalar güncel çalışma ağacında yapıldı.

## Sonuç ve lisans kapsamı

GitHub deposu inceleme başında zaten herkese açıktı; kökte lisans ve README yoktu.
Proje sahibinin tercihiyle **MIT** eklendi. Proje sahibi maskotları kendisinin
ürettiğini/ürettirdiğini doğruladı; özgün kod, belgeler ve bu görseller aynı lisansa
dahil edildi. Üçüncü taraf kodların lisansları ayrı korunur.

Uygulama sunucusu, müşteri paneli, araçlar, veri şemaları ve tanıtım sayfasının
kaynakları depoda bulunuyor. Derleme için özel bir uygulama paketi veya lisans
sunucusu gerekmiyor. Ayrı `sites/` çalışma kopyası uygulamanın derleme girdisi değil;
tanıtım sayfasının yayımlanabilir kopyası `railway/nudgy-tanitim` altında mevcut.

Bu çalışma yerel kaynak dosyalarını hazırlar. GitHub'a push, sürüm yayımlama,
Railway dağıtımı, depo ayarı değişikliği veya canlı hesaplara mesaj gönderimi
yapılmadı. Yeni GitHub iş akışları push sonrasında çalışabilir.

## İncelenen alanlar ve yapılan düzenlemeler

| Alan | Bulgu / işlem |
| --- | --- |
| Lisans | MIT, bağımlılık envanteri ve gerçek modül sürümlerinden kopyalanan lisans/NOTICE dosyaları eklendi |
| Başlangıç belgeleri | README, mimari/veri envanteri, kendi sunucunda kurulum ve katkı rehberi eklendi |
| Güvenlik bildirimi | Özel bildirim akışı ve mevcut veri saklama sınırları belgelendi |
| Git geçmişi | Gitleaks 8.30.1 ile 107 commit ve yeni dosyalar dahil yayımlanacak çalışma ağacı tarandı; gizli bilgi bulunmadı |
| Ek gizli bilgi kontrolü | Başlangıçtaki 941 Git blob'u belirli anahtar kalıpları ve yerel anahtar değerleriyle, değerler yazdırılmadan karşılaştırıldı; yalnızca örnek localhost veritabanı adresi eşleşti |
| Docker bağlamı | `.env.local` ve ayrı site çalışma kopyasını da alabilen geniş kopyalama yerine gereken kaynakları kabul eden `.dockerignore` eklendi |
| Ortam örneği | Eksik GitHub ayarları eklendi; varsayılan kişisel hatırlatıcı kapatıldı; geçersiz eski API açıklaması düzeltildi |
| Anahtar üretimi | Bağımsız 32-byte anahtarlar üreten, 0600 izinli dosya yazan ve mevcut `.env` dosyasını değiştirmeyen yardımcı eklendi |
| Veritabanı başlangıcı | İlk üç şemanın yalnızca Up bölümlerini tek transaction içinde uygulayan boş veritabanı betiği eklendi |
| Kendi sunucunda çalıştırma | Compose app profili, kalıcı Codex diski, PostgreSQL sağlık kontrolü ve yalnızca localhost portları eklendi |
| Bağımlılık güvenliği | Go 1.26.11, go-jose 4.1.4, pgx 5.9.2, x/text 0.44.0 ve çözümlemenin gerektirdiği x/sync 0.25.0 kullanıldı; artık gerekmeyen x/crypto kaldırıldı |
| Sürekli kontrol | Sabit commit'lere bağlı GitHub Actions, Go/JS/PostgreSQL testleri, Docker kurulum testi, govulncheck, Gitleaks ve Dependabot yapılandırması eklendi |
| Katkı akışı | Hata/özellik ve PR şablonları eklendi; katkı lisansı ve test beklentileri açıklandı |
| Eski belgeler | Global AI anahtarı/modeli ve eski kişisel repository kurulum akışlarını anlatan ana bölümler güncel workspace akışına uyarlandı |

İlk `govulncheck`, yerel Go 1.25.5 ile erişilebilir semboller üzerinden 44 güvenlik
bildirimi raporladı. Bu sayı 44 kanıtlanmış istismar anlamına gelmez; aracın çağrı
grafiği ve upstream etkilenen-sürüm kurallarıdır. Yamalı araç zinciri ve bağımlılıklar
ile tekrar taramada **“No vulnerabilities found.”** sonucu alındı.

İlgili birincil kaynaklar: [Go güvenlik taraması](https://go.dev/doc/security/vuln/),
[x/text bildirimi](https://pkg.go.dev/vuln/GO-2026-6629),
[pgx bildirimi](https://pkg.go.dev/vuln/GO-2026-5004),
[go-jose bildirimi](https://pkg.go.dev/vuln/GO-2026-4945).
MIT metni [OSI kaynağıyla](https://opensource.org/license/mit) karşılaştırıldı.

## Doğrulama

- Tüm Go paketlerinde `go test -race ./...`, geçici PostgreSQL bağlantısı verilerek geçti.
- `go vet ./...` geçti.
- JavaScript testlerinin 10'u da geçti.
- macOS native ve Linux amd64 sunucu derlemeleri geçti.
- Temiz PostgreSQL'de başlangıç şemaları ve sunucu başlangıç şemaları uygulandı.
- Çalışan sunucuda `/health`, `/health/db`, `/dashboard` HTTP 200 döndü.
- Başlangıç betiğini dolu şemada tekrar çalıştırmak hata verdi; mevcut şema korunarak sunucu sağlıklı kaldı.
- Ortam üreticisinde anahtar uzunluğu, anahtarların farklı olması, 0600 izinleri ve mevcut dosyanın korunması kontrol edildi.
- Compose yapılandırması, temiz Docker imajı derlemesi ve boş disklerle `up --build --wait` geçti; app ve PostgreSQL sağlıklı duruma ulaştı.
- Docker üzerinde `/health`, `/health/db`, `/dashboard` HTTP 200 döndü; uid 1000, kalıcı diskin sahipliği, lisans dosyaları ve uygulama dizininde `.env` bulunmaması kontrol edildi.
- Konteynerde sabitlenmiş `codex-cli 0.164.0` çalıştı. Bu sürüm kontrolü model çağrısı veya tam protokol kabul testi değildir.

İlk Docker denemesi bilgisayarın disk alanı dolduğu için G/Ç hatasıyla kesildi.
Yalnızca bu incelemenin geçici Go önbellekleri temizlenip Docker yeniden
başlatıldıktan sonra aynı derleme ve kurulum başarıyla tamamlandı. Kullanıcının
verileri veya önceden var olan Docker kaynakları temizlenmedi.

Ücretli canlı model çağrıları, gerçek Slack/OAuth hesap akışları ve opt-in native
Codex protokol testleri bu kontrolün parçası değildir. Otomatik testler sahte
sağlayıcı yanıtları kullanır. Tarama tüm zafiyetlerin bulunacağına dair bir garanti
veya bağımsız penetrasyon testi değildir.

## Kalan sınırlar ve sonraki işler

1. **Slack ve Raindrop tokenları:** PostgreSQL'de uygulama katmanında şifrelenmeden
   tutuluyor. Veritabanı/disk erişimi kısıtlanmalı; bu tokenlar için bağımsız anahtar,
   eski kayıtları okuyabilen geçiş ve yeniden şifreleme planı ayrıca uygulanmalı.
   Bu inceleme çalışan hesapların anahtarlarını veya kayıtlarını değiştirmedi.
2. **Harici servisler:** Slack ve AI sağlayıcısı mevcut ürün akışının parçası.
   MIT, bu servislerin veya model ağırlıklarının lisansını değiştirmez. Tamamen
   çevrimdışı kullanım, yerel model sunucusu ve Slack dışı giriş henüz yok.
3. **Saklama/silme politikası:** Konuşma, iş kuyruğu, geçmiş analiz ve native AI
   dosyaları için işletmeciye ait saklama ve silme politikası gerekir. Kapsamlı bir
   otomatik veri yaşam döngüsü yönetimi bu değişiklikte eklenmedi.
4. **Ölçekleme:** Tek uygulama replikası varsayımı sürüyor. OAuth state ve native
   runtime diskleri için çoklu replika tasarımı ayrıca yapılmalı.
5. **GitHub işletimi:** Private vulnerability reporting, secret scanning/push
   protection ve branch protection ayarlarının gerçekten etkinliği ayrıca
   doğrulanmalı. Dosya eklemek bu depo ayarlarını açmaz.
6. **Dağıtım bağımlılıkları:** Go taraması bütün Debian/Node/Codex/nginx/Postgres
   paketlerini taramaz. Yayımlanacak container için ayrıca imaj güvenlik/SBOM
   kontrolü ve üçüncü taraf lisans yükümlülüklerinin değerlendirilmesi gerekir.
7. **Tanıtım sayfası:** Mevcut demo bağlantıları ve Railway deployment kimlikleri
   kurucuya ait örneklerdir; gizli anahtar değildir. Fork yayımlarken kendi
   bağlantıların kullanılmalı. Google Fonts isteği sistem fontuna düşebilir;
   fontları yerel paketlemek istenirse OFL metinleri de eklenmeli.

Bu maddeler proje kodunun MIT olarak açılmasını engelleyen kapalı bir modül olduğu
anlamına gelmez; üretim güvenliği ve hizmet bağımsızlığı için kalan işleri gösterir.
