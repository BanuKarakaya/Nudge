# Nudgy incelemesi: uygulanan düzeltmeler

13 Kasım 2026. Kaynak: `nudgy-review.html`, b22994d için hazırlanmış inceleme.
Bu belge uygulanan kodu, doğrulama kapsamını ve henüz tamamlanmayan ürün önerilerini
ayırır. Üretime dağıtım veya canlı sağlayıcı hesaplarında işlem yapılmadı.

## 11 ana bulgu

| No | Değişiklik | Doğrulama |
| --- | --- | --- |
| 01 | Bildirim kuyruğu workspace/Jira/Trello davetlerini kabul ediyor; hatalı bildirim diğerlerini 15 saniye durdurmuyor. | Gerçek PostgreSQL deposu + onboarding servisiyle yönetici/üye davet testi |
| 02 | Jira/Trello yazma yetkisi workspace kapsamından doğrulanıyor; boş eski kişisel seçim listesine takılmıyor. | Paylaşılan kapsamda yazma ve kapsam kaldırıldıktan sonra ret testleri |
| 03 | Altı sağlayıcıda bağlantı başlangıcı link sahibinin panel oturumunu gerektiriyor; dönüş aynı canlı oturuma bağlı. Linkler tek kullanımlık ve 20 dakika. Panel çerezi OAuth yollarına da taşınıyor. | Yanlış kullanıcı, oturum değişimi, çıkış ve gerçek cookie-jar yol testleri |
| 04 | Edit işlemleri kısa veritabanı sahiplenmesi ve süreli sahiplik anahtarı kullanıyor; ağ/git sırasında havuz bağlantısı tutulmuyor. Varsayılan havuz 16 bağlantı. | Tek bağlantılı PostgreSQL havuzunda callback içinden ikinci sorgu |
| 05 | Dönen yeni OAuth token çifti `/user` kontrolünden önce kaydediliyor. | Sonraki kimlik çağrısı başarısız olduğunda yeni token korunuyor |
| 06 | Merge kanıtından assistant mesajları çıkarıldı; depo/numara birlikte doğrulanıyor. Dış yan etkiler varsayılan olarak Slack onayına bağlı; push digest ve merge kaynak/hedef sürümü sabitleniyor. | Politika, tek kullanım, sahiplik, eski revizyon, yanlış hedef ve assistant enjeksiyon testleri |
| 07 | CI/otomasyon dosya koruması sağlayıcıdan bağımsız genişletildi; eski push butonları yeni politikayı atlayamıyor. | Korunan dosya yolları ve onay testleri; dolaylı CI çalıştırma riski tehdit modelinde |
| 08 | Görev listesi ayrı küçük metadata alanını okuyor. Push sonrası tam anlık görüntü siliniyor; inceleme için değişen dosyalar kalıyor. | Bozuk büyük payload olsa da metadata listesi okunabiliyor. Dosya başına depolama henüz uygulanmadı. |
| 09 | Başarısız klonlar `failed`; eski `cloning` kayıtları süre aşımına uğruyor ve açık görev kotasından çıkıyor. | Başarısız klon regresyon testi |
| 10 | Model çıktısındaki aktif Slack etiketleri etkisiz; farklı görünen metinle link hedefi gizlenemiyor. | Kullanıcı/kanal/toplu etiket ve link testleri |
| 11 | Kuyruklarda en çok sekiz deneme ve kalıcı başarısız durum var. İmzalı uninstall/bot revocation tokenı, oturumları ve bekleyen işleri kapatıyor. | Sonraki işin açılması, kurulum izolasyonu ve token temizliği testleri |

## Eklenen diğer iyileştirmeler

- Raindrop token yenileme ve sayfalama; Slack üye listesinin sayfalanması.
- Depo adı bilinen işlemlerde yalnızca hedefin yetkisini kontrol eden sorgular;
  GitLab doğrudan proje sorgusu, GitHub hedef bulununca sayfalamayı durdurma.
- `ErrConnect` / `ErrDenied` için modele anlamlı ve güvenli hata kodları.
- HTTP süre/gövde sınırları, güvenlik başlıkları, sınırlı OAuth hız limiti ve kalıcı OAuth state.
- Slack/Raindrop token şifrelemesi, satıra bağlı AAD, anahtar kimliği ve keyring rotasyonu.
- Konteyner init süreci; kapanışta bildirim worker’ını da bekleme.
- İşlem bazında onay/otomatik/kapalı ayarı; yönetici paneli ve denetim kayıtları.
- Günlük kullanıcı/workspace token rezervasyonu ve workspace eşzamanlılık sınırı.
- JSON loglar, kimliklerle ilişkilendirilen ajan/araç işlemleri ve korumalı `/metrics`.
- Saklama süresi, kişisel JSON dışa aktarma/silme ve kullanıcıya göre ayrılmış native AI durum temizliği.
- Workspace saat dilimi, hatırlatıcı ayarları ve son kaçırılmış günlük dönemi telafi eden kalıcı claim.
- Operatörün ayarlayabildiği Responses uyumlu model adresi; localhost desteği.
- Workspace yöneticisinin araç adına göre model seçebilmesi.
- `ToolRegistry` / `WithToolModule`: kimliği sunucudan alan derlenmiş araç modülleri,
  çakışan adların reddi ve yeni yazma araçlarının varsayılan onay gerektirmesi.
- Sürümlü/checksum kontrollü migration; kullanılmayan `privateMux` üretim bağlantısının kaldırılması.
- GitLab açıklamalarının tüm araç isimlerini namespace’e çevirmesi ve ortak şemayı değiştirmemesi.
- Gofmt/Staticcheck kalite kapıları, gerçek native CLI’nin CI’da çevrimdışı test edilmesi,
  gece üç tekrarlı deterministik değerlendirme ve container zafiyet taraması.
- Changelog, yerel sürüm hazırlama betiği, işletim belgesi ve merkezi tehdit modeli.

## Doğrulama

İzole PostgreSQL üzerinde tüm Go paketleri ve yarış testleri çalıştırıldı. Native
Codex 0.164.0 ile gerçek süreç/araç izolasyonu ve model devri çevrimdışı test edildi.
Yeni eklenen veri silme, retention SQL, quota, cookie-jar ve onay revizyonu testleri
çalıştırıldı. Dashboard Node testleri ve Staticcheck geçti. Derlenen sunucunun boş
veritabanıyla açılması, sağlık/dashboard/korumalı metrics uçları ve SIGTERM kapanışı da doğrulandı.

Son kalite komutları: `make check build`, `make lint`, `git diff --check`.
PostgreSQL test değişkeni `NUDGE_TEST_DATABASE_URL`, native test değişkeni
`NUDGY_TEST_CODEX_BINARY` olarak ayarlandı; bu testler atlanmadı.

13 Kasım 2026 tekrarında Docker derlemesi ve Linux arm64 konteyner testleri geçti.
Boş PostgreSQL üzerinde 15 migration, sağlık/dashboard uçları, metrics için
401/200 erişim kontrolü, root olmayan kullanıcı, yazılabilir Codex durumu,
PID 1 olarak tini ve SIGTERM sonrası çıkış kodu 0 doğrulandı.

İlk Trivy 0.76.0 taraması 121 HIGH/CRITICAL paket-zafiyet eşleşmesi raporladı:
110 Debian, 10 npm ve 1 go-jose bulgusu. go-jose 4.1.6'ya yükseltildi;
runtime Node 22/Alpine tabanına geçirildi, kurulumdan sonra gerekmeyen npm,
Corepack ve Yarn kaldırıldı. Go derleme önbelleği geçici bellek mount'unda tutuluyor.
Yeni runtime içinde root olmayan kullanıcıyla gerçek Codex 0.164.0 araç çağrısı,
model devri ve eksik rollout kurtarma testleri; Slack imza/OIDC ve oturum testleri geçti.

Son Trivy taraması Alpine 3.24.4, Node paketleri ve Go sunucu binary'si için
**0 HIGH / 0 CRITICAL**, çıkış kodu **0** verdi; ignore listesi veya
`--ignore-unfixed` kullanılmadı. Taranan Linux arm64 imajı:
`sha256:015194bd699b20793c10763959e44987cb780f1f5879c2180c216b0c126df2cc`.
Yerel JSON raporları ve test logları `.local/review/docker-2026-11-13/` altında
saklandı (Git dışında). Disk sınırı nedeniyle geçici test imajı, konteynerler ve
bu çalışmanın derleme önbelleği temizlendi; Docker servisi açık bırakıldı.
Bu sonuç yalnızca tarama anındaki HIGH/CRITICAL veritabanı kapsamını gösterir;
Linux amd64 imajının ayrı doğrulaması CI’da yapılır.

Ücretli canlı model testleri, gerçek OAuth sağlayıcılarıyla uçtan
uca bağlantı ve canlı Slack teslimatı yapılmadı. Deterministik test başarısı canlı
model başarı oranı veya penetrasyon testi sonucu olarak sunulmuyor.

## Henüz tamamlanmayan öneriler ve sınırlar

Bunlar tamamlandı diye işaretlenmiyor:

- Panelin tam çoklu dil çevirisi: Türkçe arayüz korunuyor; AI kullanıcının diline
  cevap verebilir, fakat bütün panel/error metinleri için bir dil kataloğu yok.
- GitHub Enterprise, self-hosted GitLab ve Jira Data Center: mevcut OAuth/API/izin
  modellerinin ayrı uyarlaması gerekiyor; yalnızca özel AI adresi eklendi.
- Remote MCP/plugin yükleme: derlenmiş araç kaydı var, uzaktan eklenti çalıştırma yok.
- Canlı model bazında tekrarlı injection/araç seçimi başarı oranı ve ücretli gece
  değerlendirmesi: gece işi deterministik/native protokol testleri çalıştırıyor.
- Snapshot dosyalarını ayrı satır/nesnelere bölme: listeleme ve push sonrası temizlik
  çözüldü; aktif görevlerin dosya okuma/yazması hâlâ tüm şifreli snapshot’ı işliyor.
- Tam dağıtık trace altyapısı, geçmiş legacy handler/test kodunun tamamen silinmesi
  ve bütün uzun fonksiyonların bölünmesi bu değişiklikte bitirilmedi.
- Dependabot PR’ları birleştirilmedi; tag/release yayımlanmadı ve deploy yapılmadı.

Mutlak parasal limit sağlayıcı hesabında uygulanmalı. Kaynak kodun mevcut bir CI
pipeline’ında dolaylı çalıştırılmasını hiçbir dosya adı listesi bütünüyle engelleyemez.
Slack teslimatı kabul/kayıt arasındaki çöküşte tekrarlanabilir. Bu sınırlar ve geçiş
adımları [işletim belgesinde](operations.md) ve [tehdit modelinde](threat-model.md).
