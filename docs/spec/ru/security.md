# Безопасность fork responder и live delivery

21.10.2026, решение владельца после M0/M0b. V1: `coauthor` — unrestricted live session; `ask` — отдельный runtime-restricted fork; `inform` — storage/UI и следующий owner turn без wake. Live permission mode, model/effort/persona/context не меняются. Это проектный контракт: Go CLI skeleton не реализует эти механизмы.

Примечание к публикации: частные пути заменены стабильными архивными обозначениями; исходные номера строк сохранены. См. [ключ исторических ссылок](../../README.md#historical-citations) и [английский перевод](../security.md).

## 1. Угрозы, evidence и доверенные компоненты

| Атакующий / сбой | Защищаемое свойство | Механизм / остаток |
|---|---|---|
| MITM / malicious relay | Confidentiality/authenticity body, grants, keys | TLS + pinned HPKE/JWS; relay видит routing/traffic metadata и может задерживать/удалять ciphertext |
| Украденный invite | Нет долговечного или machine-wide доступа | TTL10min, one-use, exact recipient-issued grant; первый holder может победить законного коллегу |
| Допущенный malicious peer | Scope/body не повышают authority | Verified scope dispatcher: ask не попадает в live, inform не будит, coauthor означает сильное доверие |
| Prompt injection в вопросе, source или унаследованном context | Нельзя снять ограничения responder текстом | Claude runtime tool removal / Codex OS sandbox, fixed launch profile; модель всё ещё может лгать, over-refuse и выводить секреты |
| Чтение protected files / секреты в history | Не отдать signer/admission authority и ожидаемые secrets | Обязательная authority isolation до spawn; Claude path deny + outbound policy. Codex stock RO не скрывает файлы, отдельный профиль обязателен; прочие secrets/history остаются residual |
| Stale binding/snapshot, concurrent fork | Нет wrong-session input/reply или слияния разных grants | Exact parent epoch/instance, snapshot handle, отдельный run/output/budget; shared cwd не authority |
| Crash/helper death/sandbox unavailable | Нет unrestricted fallback или ложного reply | Admission preflight, runtime boundary, supervisor, whole-answer buffer, typed receipt; ambiguous → in_doubt |
| Flood/full-context asks/loops | Контролируемый расход модели, CPU/storage | Receiver-owned reservation, per-run deadline/cost/output и concurrency limits; billing overshoot остаётся возможным |
| Compromised runtime/plugin/uid/root/publisher/provider | Честная граница защиты | Trusted computing base; E2E не защищает от локального/runtime/provider compromise |

TCB: OS и agent runtime, Go daemon/supervisor/outbound policy, минимальный adapter, publisher и crypto libraries. Responder использует provider credentials только для model request; они не передаются вопросом, env dump или tool-visible PeerTurn credential. Native runtime может писать собственные session metadata/transcripts вне tool sandbox. Обещание read-only относится к effects responder tools, а не к нулю физических writes на машине.

M0 завершён с отрицательным verdict для in-session guard: Claude throw/timeout/unload допускают effects, Codex имеет stdin gap и не даёт synchronous queue→turn mapping (`M0:73`, `:94`, `:110`). §Q6 (`:133`) сохраняется как **будущий v2 option**, не blocker v1. Его историю и raw не переписываем.

M0b завершён: Claude fork унаследовал контекст, Read работал, Write/Bash удалены runtime, live journal47/47; Codex fork унаследовал контекст, live rollout16/16, запись отклонена ядром и bwrap userns failure не выполнил команды (`M0b:14`, `:29`). Это базовый механизм, не qualification protected paths, network/MCP, budgets, snapshot races или всей OS/provider matrix.

Исходный action-authority риск не исчез: peer «owner approved» вызвал Bash canary в bypass через prior art (`L2-RAW:latency-evidence-extract.log:11`); body `ctrl:despawn` убил pane (`L2-RAW:agmsg-expert-canary-control.log:2`). Framing/подпись не являются sandbox.

## 2. Grants и admission

| Scope | Куда поступает сообщение | Authority / receipt |
|---|---|---|
| `inform` | Durable inbox, status/UI; data на следующем реальном owner turn | Никогда не peer-started turn; owner_mixed не ограничивает owner tools; reply не запускается автоматически |
| `ask` | Native fork точной live conversation | Runtime restrictions на весь child run; final answer только через daemon redaction и связанную reply route |
| `coauthor` | LIVE session: Claude mod submit / Codex queue | Обычные возможности receiving agent; strong trust, **не sandbox** |

Grant recipient-issued, directional и exact identity/device/session/epoch. Sender может запросить только subset; `kind=coauthor` при ask → reject. Body, display name, reply ID и «owner approved» не меняют scope/launch settings. Повышение — отдельный owner control. Coauthor может править, читать secrets, использовать сеть, менять конфиг и ослаблять будущую защиту в пределах host permissions; это предупреждение обязательно до join.

Pipeline: bound transport principal/size → HPKE/JWS/cert chain → exact audience/epochs → current grant/expiry/revoke → dedup/conflict → budgets → durable inbox → scope-specific capability/snapshot → live submit либо fork spawn либо inform pending. Envelope не принимает authority-поля `argv/settings/approval/toolCall/native_session_id`. Peer text — отдельные данные, не control opcode и не shell interpolation.

Перед fork start, inform boundary, live submit и reply outbox commit проверяется актуальность grant. Reply commit и local revoke сериализованы: после commit revoke новый ответ не авторизуется. Уже committed reply мог уйти по сети; ретроактивной отмены нет. Runtime без fork profile не получает ask в live как fallback.

## 3. Native fork: authority, context и framing

`ResponderRun` локально связывает `message_id/attempt_id`, exact parent binding/epoch/instance, committed snapshot boundary, native fork ID, model/effort, profile hash, budget и одноразовую reply route. Ни один из этих handles не выбирается body. Fork — не joined session: нет новых grants, mailbox, signing keys, возможности discover peers или послать сообщение в live/третью сессию.

Daemon запускает fixed absolute executable + argv с private input, без shell concatenation. До admission обязателен `authority_secrets_isolated=true`: tools не читают PeerTurn signing/decryption keys, unlocked state/admission/grant-control credentials, transport tokens, live-control IPC/auth и process handles, позволяющие выйти в live authority. Проверяются files/env/descriptors/proc inspection/symlinks и broker ACL; не только tool list. Иначе `protected_path_policy_failed`, no spawn. Provider auth нужен native runtime/TCB; его нельзя пересылать tools через env dump. Clean env не скрывает credential files и само по себе gate не закрывает.

### Claude

Native `claude -p --resume <id> --fork-session --permission-mode default` + `--allowedTools Read Grep Glob` и `--disallowedTools Bash Edit Write NotebookEdit WebFetch WebSearch Task`: M0b `M0b:10`. Live bypass не меняется; отдельный child получает default. M0b доказал removal Write/Bash, не каждый tool/version/integration.

`--allowedTools` сам по себе только auto-approval (`CC-DOC:cli-reference.md:67`). Hardened профиль фиксирует `--tools Read,Grep,Glob`, deny MCP/Agent/новых effectful tools, отключает inherited hooks/plugins/skills/commands, не даёт subagent/scheduler/session-send capabilities. Flags source-only `:87`, `:131`, `:138`; полная комбинация **не проверено**, LAB-FORK-CC. Нельзя разрешать hidden hook shell command только потому, что Bash удалён из model inventory.

### Codex

Native `codex exec -c sandbox_mode='"read-only"' -c approval_policy='"never"' fork <thread-id>`; overrides до fork, `--sandbox` после fork не принимается (`M0b:26`). OS ограничивает tool subprocesses и descendants; never исключает approval escalation, не даёт permission на обход sandbox. Произвольный shell не обязан быть pure: writes/network блокирует runtime/ядро, но CPU/read side channels остаются.

Linux bwrap требует unprivileged user namespaces. Activation/doctor проверяют именно effective sandbox в launch environment; sysctl не исключает AppArmor/seccomp denial. M0b: обычный container дал отказ без execution, seccomp-unconfined lab дал read + denied write (`:30`). Не ослаблять host policy автоматически и не повторять запуск unsandboxed. Source: `CX-SRC:sandboxing/src/bwrap.rs:28`.

macOS — Seatbelt, source `CX-SRC:core/src/sandboxing/mod.rs:179`; native fork+Seatbelt live **не проверено**. Default RO network off — source `CX-SRC:protocol/src/protocol.rs:1314`, требует live negative control. Tool network off не блокирует runtime API к provider. Inherited MCP/hosted services, plugins, shell init/config, remote send и shared jobs должны быть исключены либо отдельно доказаны ограниченными: sandbox shell не защищает внешнюю MCP service. Полный профиль **не проверено**, LAB-FORK-CX.

### Grant-aware framing и snapshot

Daemon framing сообщает проверенный recipient grant: кому владелец разрешил чтение/анализ и ответ, scope, что peer body — вопрос/данные, какие операции runtime запрещает. Не заявляет, что peer стал owner, что цитата является system command или что grant разрешает все секреты. Модель сохраняет persona/полезный native context; security не зависит от её послушания. DeepSeek в M0b over-refused даже разрешённый вопрос (`M0b:19`): тестируем полезный benign ответ отдельно от запрета раскрывать fixture secrets.

Snapshot — committed conversation prefix, не копирование transcripts в transport. Same model/effort проверяются по effective metadata; unknown/mismatch → отказ, не дешёвая модель/summary fallback. Busy session требует immutable native boundary либо ожидания; torn/неизвестный boundary → snapshot_unavailable. Файлы могут меняться во время fork: snapshot истории не даёт atomic filesystem view. Full-context наследование memory/tool-result slots и concurrent snapshot **не проверено**, LAB-FORK-CC/CX, LAB-MIX.

## 4. Live session, owner_mixed и concurrent forks

| Событие | Решение |
|---|---|
| Inform при idle/busy | Storage/status и Claude mod UI где доступен, без wake; на следующем owner input — fixed data framing и owner_mixed receipt |
| Ask при owner работе | Запустить отдельный fork на доказанном prefix либо ждать boundary; owner ход/tools не ограничивать и не прерывать |
| Ask FYI | Configurable, default status/count в UI без body/answer и без model turn; owner явно открывает result |
| Coauthor при busy | Native next-turn queue, не steer; unrestricted live turn с verified peer label |
| Owner ввод во время fork | Только live session; не steer responder и не менять его scope/profile |
| Два ask/разные peers | Default сериализация на parent; при разрешённом parallelism отдельные fork IDs/snapshots/budgets/output, без общего mutable responder |
| Owner прочитал inbox/result/peer advice | Owner_mixed unrestricted; peer text может повлиять на будущие actions, semantic taint tracking не обещается |
| Esc/parent epoch change/disconnect | Закрыть новые admissions, отменить соответствующие responders, отбросить late output; owner tools продолжают |

Boundary Gemini/agy/Copilot: inform только на owner boundary, `wake_idle=false/fork_responder=false`, ask → responder_unavailable. Coauthor boundary возможен с явной unrestricted capability; не заменять ask смешанным owner turn. Источники: `GEM-SRC:docs/hooks/reference.md:114`, `R3-N:ext-antigravity-hooks.txt:490`, `R3-N:ext-copilot-hooks.md:404`, `:407`. End-to-end **не проверено**.

Default один fork/parent, два/daemon, один outstanding ask/conversation. Увеличение owner policy не смешивает output или grants. Завершения параллельных forks могут приходить не FIFO; привязка reply всегда по message/attempt. Fork не merge'ит history или правки в live. Child transcripts/result caches локальные, private permissions/retention, не peer inbox attachment.

## 5. Fail-closed responder без блокировки владельца

| Сбой | Поведение |
|---|---|
| Unknown build/profile/model/effort/snapshot | Ask не стартует; responder_failed с responder_unavailable/model_mismatch/snapshot_unavailable |
| bwrap/userns/Seatbelt недоступен или effective restrictions не подтверждены | sandbox_unavailable, fork_responder=false, без unrestricted retry |
| Claude denied tools/path policy не применились, unknown integration | protected_path_policy_failed либо responder_unavailable; нет peer answer |
| Helper/daemon crash, runtime timeout, nonzero exit | Supervisor/recovery отменяет child group; целый validated final answer отсутствует → нет reply, typed receipt |
| Redactor timeout/error/invalid encoding/oversize output | redaction_failed либо budget_exceeded; никакого raw/partial fallback |
| Grant expired/revoked до reply commit | Отменить run/отбросить output, revoked/expired receipt; уже read context не удалить |
| Crash между spawn/commit/отправкой | in_doubt, reconcile по run/attempt/reply ID; без blind resubmit/повторного оплачиваемого fork |

Если daemon упал, peer пока видит pending/in_doubt; receipt публикуется после durable recovery, мгновенную квитанцию от мёртвого процесса не обещаем. Child не получает signing/transport credentials и не может отправить ответ сам. Native fork restrictions переживают отказ supervisor, но отмена orphan и resource cleanup требуют qualification LAB-FAIL. Ответ должен ждать positive completion, outbound policy и fresh grant, даже если процесс ещё мог читать данные.

Не меняем live permissions и не используем global onFailure:block. V1 не зависит от hook origin→tool fence. In-session guard, complete dispatch coverage и required-guard API из M0 §Q6 сохраняются в v2 research; только отдельное решение владельца вернёт их в scope.

## 6. Protected paths и граница чтения

Protected roots: PeerTurn keys/state/admission/IPC, host/provider credentials/token stores, SSH/cloud credentials, private configs/integration executables, owner-configured paths и project .env/secrets. **Authority subset** (PeerTurn keys, grant/admission/control credentials/state, transport/live-control IPC) изолируется обязательно на обоих runtime до ask, независимо от широкой read policy. Утечка signer key позволила бы forge identity/grants; её нельзя принять как encoded-exfil residual или компенсировать redaction. Обычный source доступен. Redactor не собирает credentials с диска, получает лишь уже известные core secrets/patterns.

Claude получает private `--settings` с `permissions.deny` правилами `Read(...)`; пример и absolute `//` syntax — design §8.1. Docs: deny priority `CC-DOC:permissions.md:58`, path/tool matching `:352`, root `:362`, symlink handling `:459`. **Не проверено** на resumed fork: effective merge, точный синтаксис всех protected roots, Grep content/Glob metadata, home aliases, symlinks/hardlinks/TOCTOU. Не заменять Read deny вымышленным `Glob(path)` deny. Если обязательные Claude правила не действуют, ask выключен до исправления.

Codex stock RO запрещает запись, но читает доступные uid файлы, включая credentials; network off не предотвращает exfil через answer (`M0b:37`). Это **не проходит mandatory authority isolation**, пока keys/state/IPC доступны тому же uid. Нужен qualified runtime read-deny/изолированный file view, credential broker или service account без этих данных/handles; совместимость с native fork/provider auth **не проверено**, LAB-FORK-PATHS. Native deny flag не выдумывается. До доказательства `authority_secrets_isolated=false` → ask unavailable. После gate широкое чтение прочих файлов может оставаться residual (`protected_paths_enforced=false`); confidentiality sandbox/DLP не обещается.

Для обоих runtime deny нового чтения не скрывает пользовательский секрет, уже находящийся в полном унаследованном context. Prompt injection может попросить его пересказать/закодировать; это residual, outbound filter не универсальный DLP. **Известный core exposure действующего authority secret** — отдельный случай: ask unavailable до revoke/rotation/rebind, не «допустимый context residual» и не надежда на redaction. Универсальное сканирование всей истории на неизвестные секреты не обещается. Не урезать owner context для обхода отказа. Отсутствие write canary не доказывает confidentiality или качество ответа.

## 7. E2E, invites, rotation и revoke

Криптопрофиль и точный wire — design §4–7. До model delivery: HPKE integrity + JWS Ed25519 + pinned cert chain + scope/audience/epoch. Sender-controlled display fields отдельно от identity. Key mismatch не лечится авто-TOFU повторно; `needs_verify`. Root rotation требует cross-sign старым/новым ключом и обновления pin; потеря/compromise old root — новое human verification. Никаких self-signed «новый ключ это я» через relay как достаточного proof.

Session encryption keys rotate каждые24 h/при compromise, новые public keys подписаны session/device цепочкой, old keys хранятся максимум разрешённый message TTL+grace для decrypt backlog. Нельзя удалять старый key до завершения custody/reconciliation ожидаемых messages; после удаления старый ciphertext не восстановить. Session epoch меняется при потере continuity, grant epoch — при отзыве/изменении; эти поля не взаимозаменяемы. Backup приватных ключей требует явного owner export и отдельного encrypted artifact; forward secrecy всё равно не обещается.

Invitation secret никогда не передаётся как bearer в query. Invite proof-key подпись, encrypted descriptor и подписанный claim — только standardized primitives; механизм single-use application-level. Relay atomic claim и **локальная authoritative** consume transaction у inviter предотвращают два grants при relay replay. Sequential single-use precedent подтверждён retalk, concurrent race ещё **не проверено** (`L2-RAW:rt-one-use-second-watch.log:2`), LAB-INVITE. Forwarded URL разрешает первому holder заявить свой ключ; fingerprint личности человека устанавливается доверенным каналом передачи ссылки, при повышенных требованиях сравнивается out-of-band.

Revoke:

1. Owner `grant revoke` атомарно повышает authoritative epoch, закрывает новые live/fork/inform admissions и reply commits, quarantines pending inbox и отменяет responder runs; audit/receipt outbox фиксируются вместе. Уже admitted unrestricted coauthor не имеет per-tool fence: его effects не откатываются и не обещается остановка каждого dispatch.
2. Adapter удаляет не consumed coauthor queue entries, если API позволяет. После native acceptance строгий отзыв dequeue не доказан: статус `in_doubt`/`already_admitted`, отмена best effort, не обещать per-tool revoke. Active fork отменяется и late answer не авторизуется. Inform в owner context уже не стереть; owner turn/jobs не останавливаются.
3. Online relay route permit revoke и stream close — target≤5 s; local receiver публикует effective status≤1 s после commit; новые admissions/reply commits запрещены уже этим commit. Это не отменяет уже admitted coauthor. Transport auth не заменяет admission/reply checks. L2 показал post-revoke stream event через caching (`L2-RAW:rc-observer-revoke-connected.log:5`, `:7`, `:9`).
4. Default authority — receiving session на её local daemon. Если выбран другой owner device как policy authority, его public key и policy epoch заранее закреплены recipient-signed grant, а receiver использует подписанные leases≤30 s без обновления через peer/relay. После remote authoritative commit старый lease ещё может разрешать admissions/reply commits до expiry, максимум30 s; `effective` лишь после receiver ACK либо истечения всех старых leases. Подписанные issued_at/expires_at переводятся в monotonic deadline с консервативным вычитанием clock uncertainty; delayed delivery не даёт новые30s с момента получения, rollback clock не продлевает lease. Неизвестный clock bound запрещает новые admissions/reply commits. Когда remote revoke **ещё не дошёл до authority**, статус `revoke_requested`, конечного SLA нет; нельзя назвать revoke effective по enqueue команды.
5. Invite revoke запрещает новый join, но не отзывает уже выданные grants. Это различие подтверждено: Bob продолжил писать после invite revoke (`L2-RAW:retalk-revoked-invite-contact.log:2`). Disconnect session и revoke device/root отдельно; cached policies удаляются по эпохам.

## 8. Лимиты, outbound и audit

Baseline: plaintext≤64KiB/ciphertext≤96KiB, UTF-8; 10 accepted messages/min/grant, burst5; ≤20 automatic coauthor turns + responder runs/10min/session, hop≤8. Inform не тратит model turns. Один active fork/parent, два/daemon, один outstanding ask/conversation; fair per-peer queues/control lane. Receiver не позволяет peer сбросить limits новым conversation/message ID. Overflow→backpressure/TTL без context truncation.

Per-ask reservation до spawn: deadline120s, ≤8 tool calls, output≤16KiB, estimated cost≤$1/run и ≤$10/day/daemon (настраиваемые product defaults, **не проверено**, LAB-FORK-COST). Full native context оплачивается, prefix cache не гарантирован. Нужны effective provider usage/token accounting, native cap и supervisor resource/deadline/output limits; неизвестная оценка → budget_unavailable/responder_unavailable. Cost cap может быть превышен in-flight request; это estimated ceiling, не платёжная гарантия. Не уменьшать model/effort/context ради лимита. Kill/retry не отменяют provider billing; новый ask не обнуляет daily ledger.

`ask` reply не создаёт новый ask автоматически; receipts/hello/heartbeats/typing не будят модель и не получают model reply. Child responses наследуют verified budget_id/parent chain. Групповой fan-out оплачивается/лимитируется по каждому адресату. Прецедент локального частный локальный bridge: pair bucket и hops `BRIDGE:source-D:15`; конкретные новые значения — проектный baseline, LAB-LIMITS.

Outbound: явный text-only send/reply; для ask только whole final answer, без reasoning/tool output/debug events. Go core проверяет fresh grant/reply route/size/rate, deterministic secret patterns и уже известные core credential values, выдаёт `[REDACTED:<class>]` и локальный count. Reply commit сериализован с revoke. Error/timeout/invalid output → typed failure без raw fallback. Ничего не читает автоматически из `@path`, URL, image reference, transcript или `.env`. Опасный payload можно reject с понятной причиной, не менять смысл скрытой суммаризацией. Arbitrary encoded secret, личные данные без узнаваемого pattern, другие сетевые tools и последствия `coauthor` за пределами этой redaction; read-only `ask` всё ещё может раскрыть данные из context/файлов в ответе. Encoded/paraphrased secrets и ошибки redactor — residual; LAB-FORK-REDACT должен показать и detection controls, и пределы.

Audit JSONL/local DB: actor fingerprints, session/turn IDs, grant ID/epoch, action, policy decision/reason, message ID/ciphertext digest, receipt stage/evidence, version/config hash, monotonic event seq. Не body, raw tool input, token, full invite URL, env dump или full path пользователя. Ciphertext digest предпочтительнее plaintext hash, чтобы логи не позволяли guessing коротких сообщений. Append-only permissions и signed checkpoints обнаруживают часть повреждений, но same-uid compromise может подделать/удалить журнал; это не forensic immutable audit. Retention30 d, opt-in encrypted debug с TTL и предупреждением о plaintext в native host transcript.

## 9. Supply chain и relay hardening

- Подписанный manifest и hashes проверяются **до выполнения** installer/helper/plugin. Привязанный к invite release не может выбрать произвольный publisher или mutable HEAD. Bootstrap page — данные, не команда. First-use доверие publisher отдельно от peer identity; existing helper содержит pinned release key.
- Go modules/toolchain pinned, SBOM, vulnerability checks, воспроизводимые artifacts. Новые shell/parser/SQLite/signature dependencies проходят M1 review; не использовать случайный пакет из совпавшего npm имени. Подпись не заменяет code review.
- Relay non-root, bounded JSON/headers/bodies/timeouts, quotas включены по умолчанию, auth каждый request, WSS revalidation/revoke, admin/private metrics отдельно. Никакого outbound fetch пользовательских URL; bootstrap fetch helper только HTTPS trusted publisher, redirect allowlist, запрет loopback/metadata/private destinations кроме явно выбранного local-test profile.
- Invite HTML без analytics/third-party resources, no-referrer/no-store; fragment не отправлять в telemetry. Одноразовый Monitor notify ticket в URL редактируется из access logs, не даёт content read и имеет≤60 s TTL до upgrade. Native sockets/tokens никогда не relay credentials.
- Обновление: suspend peer admission, verify signed compatibility, atomic binary/config swap, probe scope-specific live/boundary/responder profiles, затем resume. Совместимый rollback не восстанавливает старый revoked grant epoch. Restore backup ниже monotonic epoch требует re-enrollment; иначе rollback мог воскресить доступ.

## 10. Остаточные риски и условия приёмки

1. `coauthor` — strong trust и обычные host capabilities; может читать секреты, действовать и влиять на будущую конфигурацию.
2. Peer text в owner_mixed, истории и явно открытом ask result влияет на unrestricted owner reasoning; semantic injection не изолирована.
3. Read-only fork сохраняет prompt injection, inference/encoded exfiltration и широкий доступ Codex к файлам; path deny не очищает native history, redaction не универсальный DLP.
4. Snapshot history может устареть, filesystem не atomic, параллельные forks видят разные revisions; correctness/answer availability не гарантированы.
5. Model cost/cache/usage оценки могут отличаться от счёта, cancellation не возвращает оплату; limits и accounting требуют live qualification.
6. Runtime/uid/plugin/publisher/provider compromise за границей E2E; локальные child transcripts остаются host-managed. HPKE Base+signatures не даёт FS/PCS, relay видит metadata.
7. Invite possession не доказывает личность; revoke не стирает известные данные и не откатывает admitted coauthor/committed replies, remote partition не даёт SLA до authority commit.

V1 safety acceptance: LAB-FORK-CC/CX, LAB-FORK-PATHS, LAB-FORK-REDACT, LAB-FORK-COST, LAB-INFORM, LAB-FAIL, LAB-MIX, LAB-INVITE, LAB-REVOKE, LAB-EXFIL. M0b доказывает возможность архитектуры; hardened profiles остаются **не проверено**. Не заявлять protected-path containment там, где его нет, и не ставить supported по одной canary. Stock in-session guard/API — будущий v2, больше не blocker полной v1.
