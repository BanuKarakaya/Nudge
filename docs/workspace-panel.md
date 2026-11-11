# Çalışma alanı paneli

Müşteri paneli `/dashboard` adresindedir. Eski `/workspace` GET bağlantıları buraya yönlenir. Şirket ve bireysel kullanım aynı akışı
kullanır: Nudgy’yi Slack çalışma alanına kuran kişi yöneticidir. Yönetici ortak
entegrasyonları kurar; ekip üyeleri yalnızca kendi sağlayıcı hesaplarını doğrular.
Bireysel kullanıcı hem yönetici hem üyedir.

Yönetici ve çalışan ayrı paneller kullanmaz. Menü oturumdaki role göre açılır:
Genel bakış ve Hesaplarım herkes içindir; AI ayarları, Entegrasyonlar ve Çalışma
alanı yalnızca yöneticidir. Bireysel kullanıcı kendi çalışma alanının yöneticisidir.
Eski genel analiz ekranı ve `/api/dashboard*` API’leri kapalıdır. Global anahtar ve
`X-User-ID` ile kullanıcı seçmeye izin veren `/api/v1/*` yolları da HTTP 410 döner.
Bu tokenlar müşteri paneline giriş veya veriye erişim sağlamaz.

## Çalışma alanına ait AI hesabı

Yönetici `/dashboard/ai` altında OpenRouter veya OpenAI API anahtarını kaydeder.
Anahtar sabit sağlayıcı adresinde doğrulanır; yönlendirmeler takip edilmez. Ardından
sağlayıcı kataloğundan günlük model ve isteğe bağlı gelişmiş model seçilir. İki
seçim aynıysa tek model kullanılır. Katalog erişimi, modelin Responses/tool-call
uyumluluğunu, bakiyeyi veya çıkarım iznini kanıtlamaz; gerçek kullanım hataları
ortak bir hesapla tekrar denenmez. Yeni anahtar kaydı model seçimini sıfırlar.

Web araması isteğe bağlıdır ve aynı sayfada ayrı Google AI anahtarı/modeli ister.
Anahtar yoksa araç sunulmaz; sunucunun `GEMINI_API_KEY` değeri kullanılmaz.

`workspace_ai_settings` yalnızca bir Slack çalışma alanına aittir. AES-GCM ile
şifrelenen zarf çalışma alanı kimliğini de içerir; farklı satıra kopyalanmış
şifreli anahtar açılmaz. `NUDGY_AI_ENCRYPTION_KEY` ayarlanmışsa bu anahtar,
aksi halde mevcut `FEEDBIN_ENCRYPTION_KEY` kullanılır. İlk kayıttan sonra bu seçimi
ve anahtarı değiştirmeyin; ayrı bir yeniden şifreleme geçişi gerekir.

Anahtarlar HTML’e, API yanıtlarına veya loglara yazılmaz. Üyeler model adını ve
hazırlık durumunu görebilir; anahtarı okuyamaz/değiştiremez. Aynı anda açılmış
formlarda revision kontrolü eski bir formun yeni ayarı veya silinmiş anahtarı
geri getirmesini engeller.

Her AI çağrısı sunucuda çözümlenen kullanıcı/Slack çalışma alanı bağıyla ayarları
okur. Slack yanıtı, gelişmiş modele geçiş, merge niyeti kontrolü ve zamanlanmış
Feedbin özetleri aynı çalışma alanının hesabını kullanır. `OPENROUTER_API_KEY`,
`OPENAI_API_KEY`, `CODEX_SIMPLE_MODEL`, `CODEX_COMPLEX_MODEL`, `GEMINI_API_KEY` ve
`GEMINI_MODEL` üretim çağrılarında kullanılmaz. Eksik anahtar/model, saklama hatası,
kota veya sağlayıcı hatası durumunda başka bir hesaba geçilmez.

Ayar değişikliği yeni native thread alanı başlatır; yalnızca aynı kullanıcıya daha
önce iletilmiş geçmiş taşınır. Anahtar kaldırılınca sonraki çağrılar durur. Hâlihazırda
başlamış bir sağlayıcı isteği, normal istek zaman aşımına kadar tamamlanabilir.

## Gizlilik sınırı

İşletmeci veya çalışma alanı yöneticisi için özel konuşmaları, kod farklarını ve
başka kullanıcıların tokenlarını gösteren bir ekran/API yoktur. Yeni analiz
kayıtları yalnızca durum, süre, kullanım ve araç adları gibi operasyon metadatasını
tutar; mesaj, yanıt, dosya yolu ve diff kopyalanmaz. Geçmiş kayıtlar bu geçişte
silinmez; eski API’lerden erişilemez.

Bu sunucuda işlenen bir SaaS hizmetidir, uçtan uca şifreli veya zero-knowledge bir
sistem değildir. Sohbet bağlamı, teslimat işleri ve kod görevleri hizmetin çalışması
için sunucu/veritabanında bulunur; altyapı yöneticisinin doğrudan veritabanı/disk
ayrıcalıkları uygulama yetkileriyle ortadan kalkmaz. Altyapı erişim denetimi ve veri
saklama politikası ayrıca yönetilmelidir. Normal ürün erişimi hiçbir işletmeci
anahtarıyla müşteri içeriğini okumaya izin vermez.

## Kurulum ve kullanım

1. Yönetici `/slack/install` üzerinden Nudgy’yi kurar. Slack’in doğruladığı kurucu
   kimliği kaydedilir; tarayıcı kendi çalışma alanının paneline yönlendirilir.
2. Yönetici önce **AI ayarları** bölümünden kendi anahtarını ve modellerini kaydeder.
   Ardından **Entegrasyonlar** bölümünde kullanacağı GitHub, GitLab, Jira veya Trello hesabına izin
   verir. GitHub App kurulumu ve GitHub repo izinleri bu adımda yönetici tarafından
   yapılır. Diğer servisler mevcut OAuth uygulamasını kullanır.
3. **Alanları yönet** ekranından ortak repo, proje veya panoları seçer ve
   entegrasyonu etkinleştirir. İlk başta bütün entegrasyonlar kapalıdır. Sunucuda
   yapılandırılmamış sağlayıcılar bağlanabilir olarak gösterilmez.
4. Üyeler Nudgy’nin özel mesajındaki **Paneli aç** bağlantısıyla giriş yapar;
   etkin servislerde **Hesabını doğrula** düğmesine basıp kendi hesaplarına izin
   verir. Üyeler GitHub App kurmaz ve repo/proje/pano seçmez.
5. Günlük istekler Slack’ten yapılır. `panel`, `paneli aç`, `entegrasyonlar` veya
   `dashboard` mesajı yeni bir özel panel bağlantısı gönderir. Mevcut sağlayıcı
   bağlantı komutları ve agent bağlantı araçları da aynı panele yönlendirir.

GitHub, GitLab, Jira ve Trello için ayrı otomatik kurulum davetleri yerine tek
panel daveti gönderilir. Feedbin/Raindrop kişisel bağlantı akışları korunur.
Daveti kaçıran veya bağlantısı süresi dolan kullanıcı Slack’ten `panel` isteyebilir.

Yöneticinin **Hesabım** bölümü de kendi hesabıdır; yöneticinin kişisel tokenı hiçbir
üyenin işleminde kullanılmaz. GitHub’da **Alanları yönet → GitHub kurulumuna repo
ekle / çıkar** ile uygulama erişimleri güncellenebilir. Ardından ortak alan seçimi
kaydedilir. Ortak alan değişiklikleri üyelerin hesaplarını yeniden bağlamasını
gerektirmez. Yeniden doğrulama kişisel kimliği yeniler; ortak seçimi değiştirmez.

## Yetki modeli

- Yöneticilik `(Slack workspace, installer Slack user)` bağına dayanır. Sonradan
  yapılan bir Slack yeniden kurulumu mevcut yöneticiliği başka bir üyeye geçirmez.
- Ortak ayar `(team_id, provider)` altında tutulur. Kişisel OAuth tokenları mevcut
  şifreli kullanıcı/sağlayıcı kayıtlarında kalır.
- Gerçek erişim: **etkin ortak alan seçimi ∩ kullanıcının güncel sağlayıcı izinleri**.
  GitHub için uygulama kurulumunun mevcut izinleri de ayrıca kontrol edilir.
- Okuma, dosya düzenleme, push, PR/MR, merge ve görev/kart işlemlerinde servis
  katmanı ortak kapsamı yeniden okur. Kapalı entegrasyon veya okunamayan politika
  erişim sağlamaz. Mevcut işlerin yayınlanması da güncel kapsam kontrolüne tabidir.
- Kaydedilen repo/pano/proje kimlikleri sunucuda yöneticinin canlı erişim listesinden
  doğrulanır. Kullanıcıdan gelen isim, çalışma alanı veya başka bir kullanıcı ID’si
  yetki kaynağı değildir.
- Kişisel bağlantıyı kaldırmak ortak ayarları ve diğer kişisel hesapları silmez.
  Ortak entegrasyonu kapatmak tokenları silmeden tüm çalışma alanı işlemlerini durdurur.
  Sağlayıcıdaki OAuth iznini tamamen kaldırmak için sağlayıcının uygulama izinleri
  ekranı kullanılır.

PR/MR ve tracker işlemleri isteği yapanın kendi tokenıyla yapılır. Yeni kod
incelemelerinde commit yazarı da sunucu tarafından doğrulanıp sabitlenir:
GitHub’da kimlik numaralı no-reply adresi, GitLab’da `/user` API’sinin döndürdüğü
`commit_email` kullanılır. Kimliği alınamayan bir yazar adına commit üretilmez.
Committer Nudgy olarak kalır; commit mesajındaki `Nudgy-Requested-By` alanı sağlayıcı
ve kullanıcı kimliğini belirtir. Yazar bilgisi inceleme özetine dahildir; değiştirmek
inceleme hash’ini değiştirir. Eski incelemelerin hash’leri ve yazarları korunur.

## Giriş ve oturumlar

Panel bağlantıları mevcut `NUDGE_LINK_SIGNING_KEY` ile ayrı `workspace` amacı için
imzalanır ve 20 dakika geçerlidir. Sadece kullanıcının Slack DM’ine gönderilir.
Bağlantıya GET yapmak oturum açmaz; giriş formunun tarayıcı çerezi ve imzası birlikte
doğrulanır. Giriş linkinin hash’i PostgreSQL’de tek kullanımlık olarak tüketilir;
eşzamanlı tekrarların yalnızca biri başarılı olur.

Oturumlar rastgele 256 bit token kullanır; veritabanında yalnızca hash tutulur.
Çerez HttpOnly, SameSite=Lax, HTTPS üzerinde Secure ve `/dashboard` yoluyla sınırlıdır.
Oturum süresi 12 saattir; çıkış sunucu tarafında oturumu iptal eder. Tüm değişiklikler
POST, oturuma bağlı CSRF doğrulaması ve origin kontrolü gerektirir. Panel cevapları
`no-store` ve `strict-origin` politikaları kullanır; dış betik çalıştırmaz.

`NUDGE_PUBLIC_URL` canonical origin olmalıdır; HTTPS gerekir (yalnızca localhost
üzerinde HTTP kabul edilir). Yeni bir anahtar veya OAuth callback adresi gerekmez;
mevcut sağlayıcı callback’leri kullanılır.

## Mevcut kurulumların geçişi

Bu sürüm yayımlandığında mevcut ortak AI anahtarı otomatik olarak hiçbir çalışma
alanına kopyalanmaz. Her yönetici AI ayarlarını tamamlamalıdır; o zamana kadar AI
çağrıları kapalıdır. Eski panel oturumları `/workspace` kapsamlı olduğu için yeniden
giriş gerekir; süresi dolmamış giriş linkleri `/dashboard/install` adresine taşınır.
`workspace_ai.sql` eklemeli ve idempotent olarak başlangıçta uygulanır.


`migrations/workspace.sql` sunucu başlangıcında idempotent olarak uygulanır.
Yeni tablolar: `workspace_integrations`, `workspace_sessions`, `workspace_used_links`.
`slack_installations` tablosuna `installer_slack_user_id` eklenir.

Eski sürüm kurucu kimliğini saklamadığı için geçmişteki ilk üyeyi tahminen yönetici
atamayız. Mevcut çalışma alanında kurulumu yönetecek kişi **bir kez Slack kurulumunu
yeniden yetkilendirmelidir**. Ardından panelde mevcut kişisel bağlantısını kullanarak
ortak alanları seçip etkinleştirir. Kişisel tokenlar ve görev geçmişi silinmez.
Bu geçiş tamamlanana kadar GitHub/GitLab/Jira/Trello işlemleri kapalı kalır; diğer
Nudgy işlevleri etkilenmez. Geçişi ekip kullanımından önce yöneticiyle planlayın.

## Doğrulama

- `go test ./...`
- `go test -race ./internal/coding ./internal/tracker ./internal/handler ./internal/service ./internal/secure`
- `NUDGE_TEST_DATABASE_URL=<disposable-db> go test ./internal/repository/postgres`

Testler ortak alan ile canlı kişisel erişimin kesişimini, üyenin yönetim ve başka
kullanıcı adına işlem yapamamasını, yeniden kurulumla yönetici değişmemesini,
OAuth sonrası seçim ekranının üyeye gösterilmemesini, çalışma alanı izolasyonunu,
CSRF’yi, oturum çıkışını ve eşzamanlı link tekrarlarını kapsar. Gerçek Git fixture’ı
commit yazarlığını ve tekrar denemelerde aynı commit’in kullanılmasını doğrular.
Canlı sağlayıcılarda son OAuth kontrolü için yönetici ve ayrı bir üye hesabı gerekir.

## Bu değişiklikte doğrulama

Yerel testler sağlayıcı HTTP yanıtlarını ve Codex süreç protokolünü taklit eder;
ücretli gerçek model çağrısı yapılmaz. Yönetici/üye rolleri, tenant izolasyonu,
anahtar rotasyonu/silme, no-fallback, CSRF, API kapatma ve PostgreSQL şifreleme
sınanır. Canlı kabul testi için yönetici kendi anahtarıyla kısa bir Slack yanıtı,
araç çağrısı ve seçilmişse gelişmiş model çağrısını doğrulamalıdır.

Sağlayıcı sözleşmeleri: [Codex sağlayıcı ayarları](https://learn.chatgpt.com/docs/config-file/config-reference),
[OpenRouter anahtar doğrulama](https://openrouter.ai/docs/api/api-reference/api-keys/get-current-api-key),
[OpenRouter model listesi](https://openrouter.ai/docs/api/api-reference/models/list-all-models-and-their-properties),
[Google model bilgileri](https://aistudio.google.com/api-reference/models).
