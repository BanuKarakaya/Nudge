# Müşteri dashboard’u

Nudgy’nin tek müşteri paneli `/dashboard` adresindedir. Eski bordo tasarım,
sol menü ve beyaz kartlar korunur. Oturumu olmayan kullanıcı giriş ekranında **Slack ile giriş yap** düğmesini görür.
Slack hesabıyla doğrulama sonrası kendi çalışma alanına yönlenir; yönetici rolü
kurucu kaydından okunur. Oturum 12 saat sürer. İlk kurulum için ayrı
**Slack çalışma alanına ekle** bağlantısı bulunur. Slack özel mesajındaki kişisel
panel bağlantıları da alternatif olarak çalışmaya devam eder.

| Bölüm | Erişim |
| --- | --- |
| Genel bakış | Kendi çalışma alanının hazırlık durumu ve kendi bağlantı sayısı |
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
