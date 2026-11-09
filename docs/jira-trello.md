# Jira Cloud ve Trello bağlantıları

Jira ve Trello, GitHub/GitLab’dan bağımsız olarak açılabilir. Slack kullanıcısı
özel bağlantıyı açar, servis üzerinde izin verir ve Nudgy’nin erişeceği Jira
projelerini veya Trello panolarını seçer. Seçim kaydedilmeden bağlantı etkinleşmez.

Bu sürüm hesap bağlama, bağlantı durumunu doğrulama, seçili alanları listeleme,
görev/kart listeleme ve açıklamalarını okumayı destekler. Jira’da tüm durumdaki
görevler, Trello’da açık kartlar listelenir; bilinen bir arşivlenmiş kart da kendi
seçili panosunda okunabilir. Görev/kart oluşturma, güncelleme ve webhook üzerinden
otomatik bildirim bu sürümün kapsamına dahil değildir. Jira Data Center desteklenmez.

## Sunucu ayarları

```text
NUDGE_PUBLIC_URL=https://<Nudgy host>
NUDGY_JIRA_ENABLED=true
NUDGY_JIRA_CLIENT_ID=<Jira OAuth client ID>
NUDGY_JIRA_CLIENT_SECRET=<Jira OAuth client secret>
NUDGY_TRELLO_ENABLED=true
NUDGY_TRELLO_CLIENT_ID=<Trello OAuth client ID>
NUDGY_TRELLO_CLIENT_SECRET=<Trello OAuth client secret>
NUDGY_TRACKER_ENCRYPTION_KEY=<32-byte base64 key>
```

Yalnızca kullanılacak sağlayıcının `ENABLED` değerini aç. İkisi de kapalıyken
yeni ayarlar gerekmez. Şifreleme anahtarı için bir defa `openssl rand -base64 32`
çalıştır; değeri sunucunun secret/environment ayarına kaydet. Mevcut GitHub/GitLab,
Feedbin ve bağlantı imzalama anahtarlarını değiştirme. Tracker anahtarını da
sonraki deploylarda koru; değiştirmek kayıtlı tokenların okunmasını engeller.
Secret ve tokenları Slack’e, kaynak koda veya dokümana koyma.

`tracker_connections` ve `tracker_oauth_states` tabloları ilk etkin başlangıçta
otomatik ve tekrar çalıştırılabilir biçimde oluşturulur. Mevcut tablolar değiştirilmez.

## Jira Cloud uygulaması

1. [Atlassian Developer Console](https://developer.atlassian.com/console/myapps/)
   üzerinden Nudgy için OAuth 2.0 integration oluştur.
2. Authorization → OAuth 2.0 (3LO) callback URL:
   `https://<Nudgy host>/jira/oauth/callback`.
3. Permissions bölümünde **Jira API** için `read:jira-work`, **User Identity API**
   için `read:me` kapsamlarını ekle. Nudgy ayrıca `offline_access` ister.
4. Client ID ve client secret değerlerini yukarıdaki Jira ayarlarına kaydet.
   Diğer kullanıcıların bağlanabilmesi için uygulamanın dağıtım ayarını da aç.

Nudgy erişilebilir Jira sitelerini keşfeder ve proje kimliğini site kimliğiyle
birlikte saklar. Farklı sitelerde aynı proje numarası birbirinin yerine geçmez.

[Jira OAuth 2.0 kurulumu](https://developer.atlassian.com/cloud/jira/platform/oauth-2-3lo-apps/)
ve [güncel görev arama API’si](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issue-search/).

## Trello uygulaması

1. [Trello uygulama yönetiminde](https://trello.com/power-ups/admin) Nudgy
   uygulamasının OAuth 2.0 client ayarlarını oluştur.
2. Sunucuda secret tutulacağı için **confidential** client kullan.
3. Callback URL: `https://<Nudgy host>/trello/oauth/callback`.
4. Kapsamlar: `read:member:trello`, `read:board:trello`.
   Nudgy yenileme tokenı için ayrıca `offline_access` ister.
5. OAuth 2.0 client ID ve secret değerlerini Trello ayarlarına kaydet.
   Bunlar eski Trello API key / kullanıcı tokenı değildir.

Trello akışı PKCE S256 kullanır. Token değişimi sunucuda yapılır; tokenlar URL’ye
veya tarayıcı JavaScript’ine verilmez. API çağrıları Bearer başlığı kullanır.

[Trello OAuth client ayarları](https://developer.atlassian.com/cloud/trello/guides/rest-api/oauth-2-client-configuration/)
ve [confidential client akışı](https://developer.atlassian.com/cloud/trello/guides/rest-api/oauth-2-confidential-client-usage/).

## Slack kullanımı

- `@Nudgy Jira hesabımı bağla`
- `@Nudgy Trello hesabımı bağla`
- `@Nudgy Jira projelerimi listele`
- `@Nudgy Trello panolarımı listele`
- `@Nudgy <proje adı> Jira görevlerini listele`
- `@Nudgy <pano adı> Trello kartlarını özetle`
- `@Nudgy <proje adı> içindeki ABC-123 görevini oku`

Bağlantı isteği özel DM gönderir; OAuth izni ve proje/pano seçimi kullanıcıya
aittir. Link 20 dakika, OAuth ve seçim oturumları 10 dakika geçerlidir. Seçimi
değiştirmek için yeni bağlantı iste. İptal edilen veya başarısız yeniden bağlantı
mevcut çalışan bağlantıyı değiştirmez.

Araçlar: `jira_connect`, `jira_connection_status`, `jira_projects_list`,
`jira_issues_list`, `jira_issue_read`, `trello_connect`,
`trello_connection_status`, `trello_boards_list`, `trello_cards_list`,
`trello_card_read`. Liste araçları en fazla 50 görev/kart döndürür;
`next_cursor` varsa sonraki sayfa okunmalıdır. Proje/pano keşfi 2000 alanla,
Jira site keşfi 100 siteyle sınırlıdır; sınır aşılırsa eksik liste yerine hata döner.

## Kimlik ve erişim

- Her bağlantı `(provider, user_id)` ile ayrılır; Slack kullanıcısı ve workspace
  sunucu tarafından doğrulanır. Model hesap kimliği veya API adresi seçemez.
- Tokenlar, seçilen alanlar ve bekleyen OAuth bilgileri şifreli saklanır.
  OAuth state tek kullanımlık ve tarayıcı çerezine bağlıdır. Veritabanında state
  değerinin kendisi yerine özeti saklanır.
- Token yenileme PostgreSQL satır kilidi altında yapılır; yenilenen tokenlar
  sonraki API sorguları başarısız olsa bile kaydedilir.
- Her okumada hesap kimliği, seçilen alanlar ve güncel sağlayıcı erişimi
  doğrulanır. Görev/kartın seçili proje/panoya ait olduğu ayrıca kontrol edilir.
- HTTP yönlendirmeleri takip edilmez. API hata gövdeleri ve kimlik bilgileri
  modele veya uygulama loglarına aktarılmaz.

Yönetim API’si mevcut `NUDGE_API_TOKEN` Bearer kimliği ve `X-User-ID` ile korunur:

```text
GET    /api/v1/integrations/jira/connection
DELETE /api/v1/integrations/jira/connection
GET    /api/v1/integrations/trello/connection
DELETE /api/v1/integrations/trello/connection
```

Silme yalnızca ilgili kullanıcının ilgili sağlayıcı bağlantısını ve bekleyen
oturumlarını kaldırır. Sağlayıcının kendi OAuth iznini kaldırmak için Atlassian
veya Trello bağlı uygulamalar ekranını kullan.

## Doğrulama ve canlıya alma

`go test ./...` yerel testleri çalıştırır. PostgreSQL testleri için yalnızca
atılabilir bir test veritabanını `NUDGE_TEST_DATABASE_URL` olarak ver.
Testler OAuth replay/CSRF, sağlayıcı/kullanıcı/workspace ayrımı, şifreli kalıcılık,
paralel yenileme, yetki kaybı, sayfalama ve özel DM teslimini kapsar.

Kodun deploy edilmesi ve OAuth uygulama ayarlarının tamamlanması canlı bağlantı
için gereklidir. Deploy sonrasında iki farklı Slack kullanıcısıyla ayrı alanlar
seçerek bağlan; birinin seçimi diğerinde görünmemeli. Bağlantı iptalini ve yeniden
yetkilendirmeyi kontrol et. Yerel sahte API testleri canlı OAuth onayı yerine geçmez.
