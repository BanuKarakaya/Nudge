# Jira Cloud ve Trello bağlantıları

Jira ve Trello, GitHub/GitLab’dan bağımsız olarak açılabilir. Slack kullanıcısı
özel bağlantıyı açar, servis üzerinde izin verir ve Nudgy’nin erişeceği Jira
projelerini veya Trello panolarını seçer. Seçim kaydedilmeden bağlantı etkinleşmez.

Bu sürüm hesap bağlama, bağlantı durumunu doğrulama, seçili alanları listeleme,
görev/kart listeleme ve açıklamalarını okumayı destekler. Jira’da tüm durumdaki
görevler, Trello’da açık kartlar listelenir; bilinen bir arşivlenmiş kart da kendi
seçili panosunda okunabilir. Yeni Trello kartları ve Jira görevleri oluşturulabilir;
Trello kartları aynı panonun açık listeleri arasında, Jira görevleri mevcut iş
akışının izin verdiği durumlar arasında taşınabilir. Üye/assignee atama, içerik
düzenleme, yorum, etiket, tarih ve arşivleme araçları da vardır. Trello checklist
ve Jira alt görevleri desteklenir. Webhook bildirimleri ve Jira Data Center desteklenmez.

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
3. Permissions bölümünde **Jira API** için `read:jira-work`, `write:jira-work` ve `read:jira-user`, **User Identity API**
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
4. Kapsamlar: `read:member:trello`, `read:board:trello`, `write:board:trello`.
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
- `@Nudgy Trello <pano adı> panosunun Doing listesinde “Giriş ekranını düzelt” kartı oluştur`
- `@Nudgy Trello <pano adı> panosundaki <kart> kartını Doing’den Testing’e taşı`
- `@Nudgy Jira <proje adı> projesinde “Giriş ekranını düzelt” başlıklı Task oluştur`
- `@Nudgy Jira <proje adı> projesindeki ABC-123 görevini Testing’e taşı`

Bağlantı isteği özel DM gönderir; OAuth izni ve proje/pano seçimi kullanıcıya
aittir. Link 20 dakika, OAuth ve seçim oturumları 10 dakika geçerlidir. Seçimi
değiştirmek için yeni bağlantı iste. İptal edilen veya başarısız yeniden bağlantı
mevcut çalışan bağlantıyı değiştirmez.

Araçlar: `jira_connect`, `jira_connection_status`, `jira_projects_list`,
`jira_issues_list`, `jira_issue_read`, `trello_connect`,
`trello_connection_status`, `trello_boards_list`, `trello_cards_list`,
`trello_card_read`, `trello_lists_list`, `trello_card_create`, `trello_card_move`,
`jira_create_options`, `jira_issue_transitions`, `jira_issue_create`, `jira_issue_move`.
Liste araçları en fazla 50 görev/kart döndürür;
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
- Her okuma ve değişiklikte hesap kimliği, seçilen alanlar ve güncel sağlayıcı erişimi
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

## Yazma izinlerine geçiş

Deploy öncesinde Jira uygulamasına `write:jira-work`, Trello uygulamasına
`write:board:trello` iznini ekle. Sonrasında kullanıcı Slack’ten yeniden bağlantı
istemeli, yeni izni onaylamalı ve alanlarını seçmeli. Client ID/secret veya
şifreleme anahtarını değiştirmek gerekmez. Eski bağlantılar okumaya devam eder;
yazma izni olmayan bir bağlantıyla oluşturma/taşıma yapılmaz.
`connection_status` yanıtındaki `write_enabled` bağlantının kapsamını gösterir;
sağlayıcının proje/pano yetkileri ayrıca geçerlidir.

Trello’da önce `trello_lists_list`, Jira’da `jira_create_options` ile gerçek
kimlikler keşfedilir. Jira’da görev türü seçildikten sonra aynı araçla zorunlu
alanlar okunur. Ek alanlar `fields_json` içinde API biçiminde gönderilir; yalnızca
oluşturma/geçiş ekranının izin verdiği alanlar kabul edilir. Eksik zorunlu değerler
kullanıcıdan istenir. Proje, görev türü ve üst görev bu ek alanlarla değiştirilemez.
Jira açıklamaları düz metinden Atlassian Document Format’a dönüştürülür.

Taşıma öncesinde kart/görev okunur; `status_id`, `expected_status_id` olarak
iletilir. Trello’da bu kimlik mevcut listeyi, Jira’da mevcut durumu belirtir.
Jira’da hedef durum kimliği yerine `jira_issue_transitions` sonucundaki geçiş
kimliği kullanılır. Durum değişmişse işlem durur; aynı hedefteki Trello kartı
tekrar taşınmaz. Sağlayıcı API’leri atomik durum karşılaştırması sunmadığından
son kontrol ile yazma arasında başka kullanıcıların değişiklik yapması mümkündür.

Aynı Slack olayının aynı parametrelerle tekrar işlenmesi, bağlantıda şifreli
saklanan işlem kaydıyla yedi gün boyunca engellenir (en fazla 1000 kayıt).
Yazmadan önce kayıt kalıcılaştırılır. Zaman aşımı, bağlantı kopması veya belirsiz
API yanıtında otomatik tekrar yapılmaz; sonuç okumayla kontrol edilir. Onaylanan
başarı kaydı tekrar çağrıldığında aynı sonuç döner. Aynı hesaba yeniden izin
vermek kayıtları korur; bağlantıyı silmek veya hesabı değiştirmek korumayı sıfırlar.
Bu koruma farklı yeni Slack mesajları veya değiştirilmiş parametreler arasında
aynı içeriği oluşturmayı engellemez. Belirsiz sonuçta yeni bir deneme için
kullanıcının açık yeni isteği gerekir.

API referansları: [Trello kart oluşturma ve güncelleme](https://developer.atlassian.com/cloud/trello/rest/api-group-cards/),
[Jira görev oluşturma, metadata ve geçişler](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issues/).

## Günlük görev yönetimi

Nudgy yalnızca bağlanan kullanıcının sağlayıcıdaki mevcut yetkileriyle ve seçili
pano/projelerde çalışır. Kalıcı silme, pano/proje ayarları, üye davet etme veya
kullanıcıların yetkisini değiştirme aracı yoktur.

| İşlem | Trello | Jira Cloud |
| --- | --- | --- |
| Kişi atama | Panodaki birden fazla üyeyi ekle/çıkar veya tüm atamayı değiştir | Atanabilir tek hesabı ata/değiştir veya atamayı kaldır |
| İçerik | Başlık ve açıklama | Başlık, açıklama, ortam ve düzenlenebilir özel alanlar |
| Planlama | Başlangıç/bitiş zamanı, tamamlandı işareti, pano etiketleri | Son tarih, etiket, öncelik, bileşen, sürüm ve süre tahminleri |
| Yorum | Oku, ekle, izin verilen yorumu düzenle | Oku, ekle, izin verilen yorumu düzenle |
| Alt işler | Checklist oluştur/adlandır, madde ekle/düzenle/tamamla/geri aç | Aynı projedeki bir üst göreve alt görev oluştur |
| Arşiv | Kartı arşivle/geri aç | Sağlayıcının plan ve yönetici yetkisi koşullarıyla tek görevi arşivle/geri aç |

Trello için mevcut `read:board:trello`, `write:board:trello` ve
`read:member:trello` kapsamları yeterlidir. Pano üyeliği yönetim izni gerekmez:
karta mevcut üyeleri atamak, panoya yeni üye davet etmek değildir.

Jira’da atanabilir kişileri keşfetmek için OAuth uygulamasına **`read:jira-user`**
eklenmeli ve kullanıcı tekrar bağlanmalıdır. Mevcut tokenlar diğer araçlarla
çalışmaya devam eder; bu kapsam yokken kişi araması/atama ilgili yeniden
bağlanma mesajıyla durur. Atamayı kaldırmak kullanıcı araması gerektirmez.
Jira kullanıcı araması ilk 1000 kullanıcı aralığıyla sınırlıdır; eksik isimde
aramayı daraltmak gerekir. Aynı isimdeki kişilerin kimliği tahmin edilmez.
“Ben” bağlantı durumundaki sağlayıcı hesap kimliğiyle eşleştirilir.

Yeni araçlar:

- Her iki sağlayıcı: `*_members_list`, `*_card/issue_update`,
  `*_card/issue_assign`, `*_card/issue_comments`, `*_card/issue_comment_add`,
  `*_card/issue_comment_update`, `*_card/issue_archive`, `*_card/issue_restore`.
  Buradaki `card` Trello, `issue` Jira araç adlarında kullanılır.
- Trello: `trello_labels_list`, `trello_card_checklists`,
  `trello_card_checklist_create`, `trello_card_checklist_rename`,
  `trello_card_checkitem_add`, `trello_card_checkitem_update`.
- Jira: `jira_issue_edit_fields`, `jira_subtask_create`.

Örnekler:

- “Bu karta beni ve panodaki Latif’i ekle.” (Mevcut üyeleri korur.)
- “Bu karttan beni çıkar.”
- “Açıklamayı güncelle, Bug etiketini ekle, teslim tarihini cuma 17.00 yap.”
- “Testler checklist’i oluştur, giriş ve çıkış testlerini ekle.”
- “Giriş testi maddesini tamamlandı yap.”
- “ABC-12 görevini Ayşe’ye ata ve önceliğini High yap.”
- “ABC-12 altına ‘Hata durumunu test et’ alt görevi aç.”
- “Göreve ‘Testler geçti’ yorumu ekle.”
- “Bu kartı arşivle.”

Jira güncellemesi önce edit-screen metadatasını denetler; yalnızca belirtilen
alanlar yollanır. Gerekli olsa bile değiştirilmemiş alanlar yeniden istenmez.
Trello kişi ekleme/çıkarma mevcut üyeler okunarak tek PUT ile uygulanır; modelin
iki kişiyi sırayla atarken ilk atamayı silmesi önlenir. Bu API’lerde atomik
karşılaştırmalı güncelleme bulunmadığından eşzamanlı dış değişikliklerle yarışma
olasılığı vardır. Tekrarlı yazma koruması tüm yeni işlemlerde de geçerlidir.

Jira arşivleme API’si **Premium/Enterprise ve Jira/site admin** yetkisi ister.
Bir üst görevi arşivlemek alt görevlerini de etkiler; araç bunu açıklar.
Bu araç yöneticilik yetkisi vermez. Geri açma öncesinde görev seçili projede
okunabilmelidir; Jira bu okumaya izin vermiyorsa kullanıcı Jira arayüzünden geri
açmalıdır. Arşivleme HTTP 200 dönse bile `numberOfIssuesUpdated` ve `errors`
alanları denetlenir; kısmi/belirsiz sonuç başarı olarak gösterilmez.

Kaynaklar: [Trello kart işlemleri](https://developer.atlassian.com/cloud/trello/rest/api-group-cards/),
[Trello checklist işlemleri](https://developer.atlassian.com/cloud/trello/rest/api-group-checklists/),
[Jira atanabilir kullanıcı araması](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-user-search/),
[Jira görev işlemleri ve arşivleme](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issues/),
[Jira yorumları](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issue-comments/).
