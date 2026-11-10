# Çalışma alanı paneli

Müşteri paneli `/workspace` adresindedir. Şirket ve bireysel kullanım aynı akışı
kullanır: Nudgy’yi Slack çalışma alanına kuran kişi yöneticidir. Yönetici ortak
entegrasyonları kurar; ekip üyeleri yalnızca kendi sağlayıcı hesaplarını doğrular.
Bireysel kullanıcı hem yönetici hem üyedir.

`/dashboard` hâlâ Nudgy işletmecisinin bütün kurulumları görebildiği özel analiz
panelidir. Dashboard/API tokenları müşteri panelinde giriş yöntemi değildir ve
çalışanlara dağıtılmaz.

## Kurulum ve kullanım

1. Yönetici `/slack/install` üzerinden Nudgy’yi kurar. Slack’in doğruladığı kurucu
   kimliği kaydedilir; tarayıcı kendi çalışma alanının paneline yönlendirilir.
2. Yönetici panelde kullanacağı GitHub, GitLab, Jira veya Trello hesabına izin
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
Çerez HttpOnly, SameSite=Lax, HTTPS üzerinde Secure ve `/workspace` yoluyla sınırlıdır.
Oturum süresi 12 saattir; çıkış sunucu tarafında oturumu iptal eder. Tüm değişiklikler
POST, oturuma bağlı CSRF doğrulaması ve origin kontrolü gerektirir. Panel cevapları
`no-store` ve `strict-origin` politikaları kullanır; dış betik çalıştırmaz.

`NUDGE_PUBLIC_URL` canonical origin olmalıdır; HTTPS gerekir (yalnızca localhost
üzerinde HTTP kabul edilir). Yeni bir anahtar veya OAuth callback adresi gerekmez;
mevcut sağlayıcı callback’leri kullanılır.

## Mevcut kurulumların geçişi

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
