# Müşteri dashboard’u

Nudgy’nin tek müşteri paneli `/dashboard` adresindedir. Açık zemin, koyu sol menü, kompakt metrik kartları ve grafikler kullanılır. Oturumu olmayan kullanıcı giriş ekranında **Slack ile giriş yap** düğmesini görür.
Slack hesabıyla doğrulama sonrası kendi çalışma alanına yönlenir; yönetici rolü
kurucu kaydından okunur. Oturum 12 saat sürer. İlk kurulum için ayrı
**Slack çalışma alanına ekle** bağlantısı bulunur. Slack özel mesajındaki kişisel
panel bağlantıları da alternatif olarak çalışmaya devam eder.

| Bölüm | Erişim |
| --- | --- |
| Genel bakış | Kendi istek/token toplamları, yanıt süresi, sorun oranı, günlük kullanım, model dağılımı ve son 5 işlem; yönetici ayrıca çalışma alanı toplamlarını seçebilir |
| Kurulum | AI hazırlık durumu ve isteğe bağlı bağlantılar; yönetim adımları yalnızca yöneticiye görünür |
| İşlemlerim | Kendi istekleri, mesaj/yanıt, model çağrıları ve araç adımları; tarih/durum/model filtreleri, 25 kayıtlık sayfalar |
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

Ortak CSS, müşteri paneli CSS/JS dosyaları ve maskot/profil/emoji görselleri statik sunulur. Eski
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
Yönetici çalışma alanı kapsamında ekip işlemlerinin metadata’sını görebilir;
başka bir üyenin konuşma veya model içeriğini göremez. Liste sorguları içerik
getirmez. İstek numarasıyla seçilen kayıtta yalnızca isteğin sahibine mesaj,
yanıt ve şifreli/maskelenmiş model içeriği açılır.

Kullanım sağlayıcının bildirdiği tokenlara dayanır. Cache ve reasoning ayrı
gösterilir. Maliyet yalnızca sağlayıcı `usage.cost` bildirdiğinde USD olarak
kaydedilir; kapsanan çağrı sayısı belirtilir, eksik maliyet sıfır sayılmaz.
Yeni çağrılar model bazında ayrılır; eski kayıtlarda model zinciri birlikte kalabilir. Günler
Türkiye saatine göre hesaplanır; dönemler son 24 saat/7/30/90 gün veya tüm zamandır.
Yeni kayıt bulunmaması ile veri okuma hatası farklı durumlar olarak gösterilir.

Otomatik karşılama daveti artık kalıcı `/dashboard` bağlantısını ve **Slack ile
giriş yap → Hesaplarım** adımlarını içerir. Link kişisel token içermez. Diğer
servislerin OAuth bağlantıları kısa ömürlü olmaya devam eder. Davet tekilleştirme
anahtarı değiştirilmedi: eski kullanıcılara toplu yeniden mesaj gönderilmez ve
önceden gönderilmiş mesajlar düzenlenmez. İstenirse Slack’teki `panel` komutu
önceki kişisel hızlı giriş bağlantısını da üretmeye devam eder.

Yeni istek inceleyicisi ve Slack kurulum dosyaları için [kurulum/geçiş notları](feedback-fixes.md).
