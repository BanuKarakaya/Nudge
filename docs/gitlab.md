# GitLab.com bağlantısı ve kod düzenleme

GitLab desteği GitHub ile birlikte veya tek başına etkinleştirilebilir. Kullanıcı
Slack’te “GitLab hesabımı bağla” dediğinde özel OAuth bağlantısı alır; GitLab’da
izin verdikten sonra Nudgy ekranında erişilecek projeleri seçer. Bağlantı ancak
bu seçim kaydedildikten sonra tamamlanır. Bağlantı isteği kod görevi başlatmaz.

## Bir defalık kurulum

GitLab.com’da **Edit profile → Access → Applications → Add new application**
üzerinden Nudgy için bir OAuth uygulaması oluştur:

- Redirect URI: `https://<NUDGE_PUBLIC_URL-host>/gitlab/oauth/callback`.
- **Confidential** açık olmalı; uygulama sırrı yalnızca sunucuda kullanılır.
- Scopes: `read_user`, `read_api`, `write_repository`, **`api`**.
- Application ID ve Secret değerlerini Railway servis değişkenlerine kaydet;
  Slack mesajına veya repoya ekleme.

```text
NUDGY_GITLAB_ENABLED=true
NUDGY_GITLAB_CLIENT_ID=<Application ID>
NUDGY_GITLAB_CLIENT_SECRET=<Secret>
NUDGE_PUBLIC_URL=https://<Nudgy host>
NUDGY_CODING_ENCRYPTION_KEY=<mevcut 32-byte base64 anahtar>
```

Mevcut şifreleme anahtarını değiştirme. GitHub’ın `NUDGY_CODING_ENABLED` ve
`NUDGY_GITHUB_*` ayarları bağımsızdır. Yeni GitLab tabloları başlangıçta otomatik
oluşturulur. OAuth yetkisi GitLab tarafında kullanıcı kapsamındadır; seçilen
projelerle sınırlama Nudgy tarafından her listeleme, okuma, düzenleme, inceleme
ve push işleminde ayrıca uygulanır. GitLab.com dışındaki sunucular desteklenmez.

[GitLab OAuth uygulama belgesi](https://docs.gitlab.com/integration/oauth_provider/)
ve [PKCE/token yenileme belgesi](https://docs.gitlab.com/api/oauth2/).

## Kullanım

1. Slack’te `@Nudgy GitLab hesabımı bağla` yaz.
2. Özel bağlantıyı aç, GitLab’da izin ver, Nudgy’de projeleri seç ve kaydet.
3. `@Nudgy GitLab projelerimi listele` ile seçimini kontrol et.
4. Projenin tam yolunu ve istenen değişikliği yaz. Alt gruplar desteklenir:
   `team/mobile/ios-app`.
5. Nudgy kaynak kodu düzenler ve aynı projenin yeni `codex/nudgy-<görev>` dalına
   otomatik pushlar. Mesajdaki dal bağlantısından clone/fetch yaparak test et.
   Otomatik test çalıştırılmaz. MR açma ayrıca istenebilir; merge için kaynak/hedef dalı veya MR adresini belirten açık
   bir komut gerekir. Ayrıntılar: [PR/MR işlemleri](change-requests.md).

Yeni proje eklemek veya seçimi değiştirmek için yeni bir bağlantı iste.
Başarısız/iptal edilen bağlantı mevcut çalışan bağlantıyı değiştirmez.
Proje seçimi OAuth izninden sonra 10 dakika geçerlidir; inceleme ZIP bağlantısı
24 saat geçerlidir. Yayınlanmış dalın süresi dolmaz.

## Kimlik ve erişim sınırları

- GitHub ve GitLab bağlantıları, OAuth oturumları ve görevleri ayrı tablolardadır.
  Aynı sayısal hesap/proje kimliği farklı servislerde eşleşme sayılmaz.
- Slack kullanıcısı ve workspace sunucu tarafından doğrulanır. Model kullanıcı,
  erişim tokenı, API adresi veya hedef dal seçemez.
- Her işlem tokenın `/user` kimliğini yeniden doğrular; seçilmiş projede en az
  Developer yetkisi gerekir. Arşivlenmiş/boş projeler listelenmez. Korumalı dal
  veya ek GitLab politikaları pushu engellerse başarı bildirimi yapılmaz.
- OAuth PKCE ve tek kullanımlık, tarayıcı çerezine bağlı state kullanır. Seçim
  tamamlanana kadar tokenlar yalnızca şifreli geçici oturumda saklanır.
- Tokenlar ve yenileme tokenları şifreli saklanır; yenileme satır kilidi altında
  yapılır ve seçilen proje listesi korunur. Git erişimi yalnızca HTTPS üzerinden
  gitlab.com’a gider; yönlendirmeler ve depo hook/script çalıştırma kapalıdır.
- GitLab bağlantısını kaldırmak GitHub bağlantısını veya GitHub görevlerini
  etkilemez. GitLab’daki OAuth izni ayrıca GitLab Applications ekranından iptal
  edilebilir.

Araçlar `gitlab_connect`, `gitlab_connection_status`, `gitlab_repositories_list`,
`gitlab_repository_task_prepare`, `gitlab_repository_task_status`,
`gitlab_repository_task_cancel`, `gitlab_repository_tasks_list`,
`gitlab_repository_file_read`, `gitlab_repository_file_write`,
`gitlab_repository_edit_finish`, `gitlab_repository_change_request_create`,
`gitlab_repository_change_request_status`, `gitlab_repository_change_request_merge`,
`gitlab_repository_branches_list`, `gitlab_repository_branch_change_request_create`,
`gitlab_repository_branches_merge`
adlarıyla mevcut GitHub araçları yanında sunulur.

## Doğrulama

Yerel testler OAuth/CSRF/replay, proje seçimi, yetki kaybı, kimlik değişimi,
sağlayıcı ayrımı, token yenileme, HTTP hata/pagination, gerçek Git ile ayrı dala
push ve PostgreSQL şifreleme/yeniden başlatma/bağlantı kaldırmayı kapsar.
Canlı doğrulama için OAuth ayarları ve deploy sonrası iki ayrı Slack/GitLab
hesabıyla farklı proje seçimleri yapılmalı; diğer kullanıcının projeleri
listelenmemeli ve test değişikliği yalnızca görev dalında görünmelidir.

## PR/MR yetkisi yükseltmesi

MR oluşturma, durum sorgulama ve merge araçları `api` kapsamını gerektirir.
Nudgy OAuth isteği artık bu kapsamı da talep eder. GitLab uygulamasında `api`
seçeneğini açtıktan ve yeni sürümü yayınladıktan sonra kullanıcı Slack’te
“GitLab hesabımı bağla” diyerek yeniden yetkilendirip projelerini seçmelidir.
Uygulama ayarını değiştirmek eski token’ın yetkisini kendiliğinden yükseltmez.
Client ID, secret veya şifreleme anahtarını değiştirmek gerekmez.

Eski bağlantılar listeleme, düzenleme ve push için çalışmaya devam eder.
`api` izni olmayan veya kapsamı eski kayıtta bilinmeyen bağlantıların MR araçları,
yeniden bağlantı kurulmasını açıklayan hata döndürür. Yenileme token’ı eski
kapsamla çalışmaya devam eder; yenileme tek başına yetki yükseltmez.
`api` kullanıcı hesabının geniş API yetkisidir; Nudgy seçilmiş proje ve güncel
kullanıcı erişim kontrollerini her PR/MR işleminde ayrıca uygular.

Kullanım ve merge komutları: [PR/MR işlemleri](change-requests.md).
