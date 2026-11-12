# Müşteri dashboard’u

Nudgy’nin tek müşteri paneli `/dashboard` adresindedir. Eski bordo tasarım,
sol menü ve beyaz kartlar korunur. Oturumu olmayan kullanıcı giriş ekranında **Slack ile giriş yap** düğmesini görür.
Slack hesabıyla doğrulama sonrası kendi çalışma alanına yönlenir; yönetici rolü
kurucu kaydından okunur. Oturum 12 saat sürer. İlk kurulum için ayrı
**Slack çalışma alanına ekle** bağlantısı bulunur. Slack özel mesajındaki kişisel
panel bağlantıları da alternatif olarak çalışmaya devam eder.

| Bölüm | Erişim |
| --- | --- |
| Genel bakış | Kendi istek/token toplamları, yanıt süresi, sorun oranı, günlük kullanım, model dağılımı ve son 5 işlem; yönetici ayrıca çalışma alanı toplamlarını seçebilir |
| Kurulum | AI hazırlık durumu ve isteğe bağlı bağlantılar; yönetim adımları yalnızca yöneticiye görünür |
| İşlemlerim | Kendi istekleri ve içerik içermeyen araç adımları; tarih/durum/model filtreleri, 25 kayıtlık sayfalar |
| Kullanım | Kendi token ve model kullanımı; yönetici ayrıca yalnızca kendi çalışma alanının toplu sayaçlarını görebilir |
| AI ayarları | Yalnızca yönetici; sağlayıcı, şifreli anahtar, model seçimi |
| Entegrasyonlar | Yalnızca yönetici; ortak repo/proje/pano kapsamı |
| Hesaplarım | Her kullanıcı; yalnızca kendi servis hesapları |
| Çalışma alanı | Yalnızca yönetici; kendi Slack çalışma alanının kayıtlı üyeleri |

Bireysel kullanım aynı panelde yönetici erişimiyle çalışır. Yönetici olmak özel
konuşmaları veya üyelerin kişisel anahtarlarını görme hakkı vermez.

Eski tüm-çalışma-alanları analiz uygulaması ve global API’ler sunulmaz:
`/api/dashboard`, `/api/dashboard/*` ve `/api/v1/*` HTTP 410 döner. Eski dashboard
tokenları veya API tokenları yeni oturuma dönüştürülmez. `/dashboard/index.html`
ve eski branch sayfası ana panele yönlenir. Mevcut branch kuralları saklanır;
global kurallar editörü sunulmaz. `/workspace` GET bağlantıları yeni adrese taşınır.

Yalnızca ortak CSS, müşteri paneli CSS’i ve maskot dosyası statik sunulur. Eski
analiz JavaScript’i ve HTML’i artık erişilebilir dosya yolları değildir.

AI anahtarı saklama, sağlayıcı doğrulama, model yönlendirme, gizlilik sınırı ve
geçiş adımları için [çalışma alanı paneli](workspace-panel.md) belgesine bakın.

Genel bakış `/dashboard`, kurulum `/dashboard/setup`, işlemler `/dashboard/operations`, kullanım `/dashboard/usage` adresindedir.
Kurulum adımları ana ekranda tekrarlanmaz. AI ayarı eksikse Genel bakış üzerinde küçük bir hatırlatma gösterilir; isteğe bağlı servislerin bağlı olmaması eksiklik sayılmaz.
Analiz sayfaları mevcut müşteri oturumunu kullanır; query parametreleri veya
operatör tokenı kullanıcı/çalışma alanı seçemez. Repository kimliği ve rolü
veritabanından tekrar çözer. Kişisel kayıtlar team + Slack user ile eşleştirilir;
varsa dahili user ID de aynı olmalıdır. Dahili ID’si olmayan eski kayıtlar bu
Slack kimliği eşleşmesiyle görülebilir. Eşleştirilemeyen eski kayıtlar gösterilmez.
Yönetici çalışma alanı toplamlarını seçtiğinde hiçbir kişisel işlem listesi
getirilmez. Eski satırlardaki mesaj, yanıt, path, diff ve araç sonuçları dahi
müşteri sorgusuna dahil edilmez; yalnızca güvenli işlem metadata’sı seçilir.

Kullanım sağlayıcının bildirdiği tokenlara dayanır; fiyat/bakiye/fatura değildir.
Birden çok model kullanılan istekler birleşik model adıyla gruplanır. Günler
Türkiye saatine göre hesaplanır; dönemler son 24 saat/7/30/90 gün veya tüm zamandır.
Yeni kayıt bulunmaması ile veri okuma hatası farklı durumlar olarak gösterilir.

Otomatik karşılama daveti artık kalıcı `/dashboard` bağlantısını ve **Slack ile
giriş yap → Hesaplarım** adımlarını içerir. Link kişisel token içermez. Diğer
servislerin OAuth bağlantıları kısa ömürlü olmaya devam eder. Davet tekilleştirme
anahtarı değiştirilmedi: eski kullanıcılara toplu yeniden mesaj gönderilmez ve
önceden gönderilmiş mesajlar düzenlenmez. İstenirse Slack’teki `panel` komutu
önceki kişisel hızlı giriş bağlantısını da üretmeye devam eder.
