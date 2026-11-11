# karusel-tuner

Телеканал «Карусель» (веб-эфир) как ТВ-тюнер для **Plex**. Сервис принимает поток через отдельный VPN-туннель и сам выглядит для Plex как тюнер **HDHomeRun**. Программу передач он отдаёт в формате **XMLTV**, так что Plex DVR может записывать передачи по расписанию (для DVR нужен Plex Pass).

**Статус: MVP.** Проверено на Docker Desktop (macOS) с VPN владельца. Туннель поднимается, поток идёт (H.264 1080p + AAC, ≈ 7 Мбит/с), программа передач загружается через VPN и совпадает с эфиром по времени.

**Не проверено:**
- запись в Plex от начала до конца;
- что в 21:00 МСК в веб-эфире идёт «Спокойной ночи, малыши!», а не заставка.

Проект не обходит DRM и не перепубликует поток в интернет.

## Как это работает

```text
OPENVPN_CONFIG_BASE64 ──> vpn-config (однократно): профиль + DNS-имя remote -> IPv4
                                   │
                         Gluetun / OpenVPN (только для сервиса)
                                   │
      плеер MediaVitrina ──> relay (Go): ссылка у балансера -> Streamlink (HLS)
      karusel-tv.ru ───────> relay (Go): расписание -> XMLTV
                                   │
             127.0.0.1:18080: /live.ts, /discover.json, /lineup.json, /epg.xml
                                   │
                        Plex Live TV & DVR (тюнер HDHomeRun + XMLTV)
```

Через VPN ходит **только** сервис. Plex и остальной хост работают через обычную сеть. Выход VPN должен быть в России: плеер MediaVitrina пускает только российские адреса.

## Что нужно

- Linux-хост с Docker Engine и Compose v2, устройство `/dev/net/tun`. Docker Desktop на macOS тоже подходит.
- OpenVPN-профиль (`.ovpn`), у которого есть легальный доступ к эфиру. Сертификаты и ключи должны быть **внутри** профиля (`<ca>`, `<cert>`, `<key>`). Имя хоста в `remote` (например, DDNS) допустимо: при каждом развёртывании оно само заменяется на IPv4.
- Plex Media Server с Plex Pass. Лучше на этом же хосте и с `network_mode: host`.

## Установка через Portainer

Образ `ghcr.io/aquare11e/karusel-tuner` (amd64 и arm64) собирается автоматически из этого репозитория.

1. Закодируйте профиль в base64 одной строкой:
   ```bash
   base64 -i client.ovpn | pbcopy     # macOS: результат сразу в буфере обмена
   base64 -w0 client.ovpn             # Linux
   ```
2. Portainer → **Stacks → Add stack**, имя `karusel-tuner`. Задайте compose-файл одним из способов:
   - **Repository**: URL `https://github.com/aquare11e/karusel-tuner`, reference `refs/heads/main`, compose path `compose.portainer.yaml`;
   - **Web editor**: вставьте содержимое [`compose.portainer.yaml`](compose.portainer.yaml).
3. **Environment variables**:

   | Переменная | Значение |
   | --- | --- |
   | `OPENVPN_CONFIG_BASE64` | строка из шага 1 (**обязательно**) |
   | `OPENVPN_USER` | логин VPN, если профиль использует `auth-user-pass` |
   | `OPENVPN_PASSWORD` | пароль VPN |

   Остальные параметры необязательны, см. таблицу [ниже](#параметры).
4. **Deploy the stack**. Нормальное состояние:
   - `vpn-config` один раз отработал и завершился (`exited - code 0`);
   - `vpn` в статусе `healthy`;
   - `relay` в статусе `running`.
5. Подключите тюнер в Plex ([ниже](#подключение-plex)) и проверьте работу ([ниже](#проверка)).

**Обновление.** В стеке нажмите «Pull and redeploy» или «Update the stack» с включённым «Re-pull image». При каждом развёртывании `vpn-config` заново резолвит DNS-имя VPN-сервера. Обычный перезапуск контейнеров использует прежний адрес.

> **Если Portainer не может скачать образ.** Посмотрите, завершилась ли сборка во вкладке Actions репозитория. Если пакет GHCR приватный, сделайте его публичным (GitHub → Packages → karusel-tuner → Package settings → Change visibility) или добавьте учётные данные GHCR в Portainer → Registries.

> Профиль и пароль видны всем, у кого есть доступ к этому Portainer или к `docker inspect` на хосте.

## Установка из исходников

```bash
git clone https://github.com/aquare11e/karusel-tuner.git && cd karusel-tuner
cp .env.example .env && chmod 600 .env
# в .env: OPENVPN_CONFIG_BASE64=<base64 профиля>, OPENVPN_USER, OPENVPN_PASSWORD
make deploy        # сборка образа и запуск vpn-config, vpn, relay
```

Вместо `OPENVPN_CONFIG_BASE64` можно положить профиль в `vpn/client.ovpn`: он используется, если переменная пуста. Не добавляйте `.env`, `.ovpn` и ключи в Git: они уже в `.gitignore`.

## Подключение Plex

Plex в Docker с `network_mode: host` на той же машине видит сервис по адресу `127.0.0.1:18080`.

1. Plex → **Настройки → Live TV & DVR → Set up Plex DVR**.
2. Сам тюнер не найдётся: широковещательный поиск не поддерживается. Нажмите **«Don't see your HDHomeRun device? Enter its network address manually»** и введите `127.0.0.1:18080`.
3. Появится один канал — **1 «Карусель»**.
4. Программа передач: **«Have an XMLTV guide on your server? Click here to use that instead»** → `http://127.0.0.1:18080/epg.xml`. Если Plex не сопоставил канал `1` с «Карусель» сам, сделайте это вручную.
5. Выберите библиотеку «ТВ-передачи» для записей.
6. В гиде найдите передачу → **Record → Record Series**. Рекомендуемые отступы: начинать на 2 минуты раньше, заканчивать на 3 минуты позже.

Каждый выпуск в программе — отдельная серия с датой эфира: `S2026E1030` = 30 октября 2026. Так Plex записывает выпуск каждый день и подписывает файлы датой. Выпуск ≈ 23 минуты с отступами занимает ≈ 1.2 ГБ. Передачи на «Карусели» идут блоками: например, «Барбоскины» — это несколько часов одним блоком, отдельную серию из блока не вырезать.

Plex на другой машине или в bridge-сети: см. [docs/deployment-linux.md](docs/deployment-linux.md). Открывать сервис в LAN можно только за файрволом: аутентификации нет.

## Проверка

Команды выполняются на Docker-хосте.

```bash
# 1. VPN: в логе есть "Public IP address is … (Russian Federation, …)"
docker logs karusel-tuner-vpn-config-1      # "profile from OPENVPN_CONFIG_BASE64 written …"
docker logs karusel-tuner-vpn-1 2>&1 | grep -E 'Initialization Sequence Completed|Public IP'

# 2. Через VPN ходит только сервис: первая команда выводит RU, вторая — страну хоста
docker exec karusel-tuner-relay-1 python3 -c 'import json,urllib.request;print(json.load(urllib.request.urlopen("https://ipinfo.io/json"))["country"])'
curl -s https://ipinfo.io/country

# 3. Сервис и программа: в блоке epg поле programmes > 0, last_error пустой
curl -s http://127.0.0.1:18080/status
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:18080/epg.xml    # 200

# 4. Что сейчас идёт по программе
curl -s http://127.0.0.1:18080/epg.xml | python3 -c 'import sys,re,datetime as d
now=d.datetime.now(d.timezone(d.timedelta(hours=3))).strftime("%Y%m%d%H%M%S")
for s,e,t in re.findall(r"<programme start=\"(\d+) \+0300\" stop=\"(\d+) \+0300\"[^>]*>\s*<title[^>]*>([^<]*)<",sys.stdin.read()):
    if e>now and s<=now[:8]+"235959": print(("СЕЙЧАС " if s<=now else "       ")+s[8:10]+":"+s[10:12]+"–"+e[8:10]+":"+e[10:12]+" МСК  "+t)'

# 5. Что реально в эфире: 10-секундный клип (~15 с), открыть в любом плеере
docker exec karusel-tuner-relay-1 ffmpeg -v error -y -i http://127.0.0.1:8080/live.ts -t 10 -c copy /tmp/clip.mp4 \
  && docker cp karusel-tuner-relay-1:/tmp/clip.mp4 ./karusel-clip.mp4
```

Смотреть вживую можно в VLC: «Открыть URL» → `http://127.0.0.1:18080/live.ts`.

В первую минуту после старта `/epg.xml` может отвечать 503: сервис стартует раньше туннеля, и первая загрузка программы не проходит. Повтор будет через минуту. `/healthz` говорит только, что сервер отвечает, а не что работают VPN и источник.

При установке из исходников то же самое делают `make smoke` (эндпоинты) и `make smoke-video` (≈ 20 секунд эфира + ffprobe).

Ошибки в логе `relay`:
- `geo-restricted placeholder` — выход VPN не в России;
- `no unprotected HLS stream offered` — остался только поток с DRM, это не поддерживается;
- `streams API URL not found` — изменился плеер.

Остальное — в [docs/troubleshooting.md](docs/troubleshooting.md).

## Обновление с karusel-plex-relay

Проект переименован. Старые контейнеры не подхватываются, их нужно остановить:

```bash
docker compose -p karusel-plex-relay down
```

После этого разверните новую версию через Portainer или `make deploy`. Профиль переносится в `OPENVPN_CONFIG_BASE64`; `vpn/client.ovpn` тоже продолжает работать. `DeviceID` тюнера не изменился: Plex продолжит работать с ним по прежнему адресу, изменится только название — «Karusel Tuner».

## Локальный стенд с Plex (macOS)

`compose.local-plex.yaml` добавляет контейнер Plex для проверки тюнера, гида и записи:

```bash
docker compose -f compose.yaml -f compose.local-plex.yaml pull plex
docker compose -f compose.yaml -f compose.local-plex.yaml build relay
# только теперь взять PLEX_CLAIM с https://plex.tv/claim (живёт 4 минуты) и вписать в .env
make local-up
open http://127.0.0.1:32400/web     # тюнер vpn:8080, XMLTV http://vpn:8080/epg.xml
make local-down
```

Если сервер не привязался к аккаунту, получите новый `PLEX_CLAIM`, выполните `make local-down`, удалите том `karusel-tuner_plex-local-config` и запустите снова.

## API

| Метод | URL | Назначение |
| --- | --- | --- |
| GET | `/healthz` | HTTP liveness (не проверяет VPN) |
| GET | `/status` | JSON: зрители, лимит, uptime, `upstream_restarts`, `epg` |
| GET | `/playlist.m3u` | IPTV-плейлист |
| GET/HEAD | `/live.ts` | MPEG-TS; при обрыве источника Streamlink перезапускается в том же ответе. HEAD не запускает поток |
| GET | `/epg.xml` | Программа XMLTV; 503 до первой удачной загрузки |
| GET | `/discover.json`, `/lineup.json`, `/lineup_status.json` | Тюнер HDHomeRun для Plex |
| POST | `/lineup.post` | Запрос сканирования каналов (ничего не делает) |

## Параметры

| Переменная | По умолчанию | Назначение |
| --- | --- | --- |
| `OPENVPN_CONFIG_BASE64` | пусто | `.ovpn` в base64 одной строкой. Если пусто, берётся `vpn/client.ovpn`; в Portainer обязательна |
| `OPENVPN_USER` / `OPENVPN_PASSWORD` | пусто | Логин и пароль OpenVPN, если нужны |
| `RELAY_PORT` | `18080` | Порт на хосте (127.0.0.1) |
| `RELAY_EXTRA_IP` | `127.0.0.1` (ничего не добавляет) | Только в `compose.portainer.yaml`: ещё один адрес хоста для того же порта, например шлюз `docker0` для клиентов из других Docker-сетей. Аутентификации нет: не указывайте адрес в LAN без файрвола |
| `PUBLIC_BASE_URL` | `http://127.0.0.1:18080` | Адрес сервиса в ответах тюнера и в M3U: Plex ходит по нему |
| `MAX_VIEWERS` | `2` | Тюнеров для Plex = одновременных потоков (запись + просмотр) |
| `STARTUP_TIMEOUT` | `45s` | Ожидание первых данных до HTTP 502 |
| `RECONNECT_WINDOW` | `5m` | Сколько перезапускать Streamlink после обрыва посреди записи |
| `SOURCE_URL` | плеер MediaVitrina | Источник; **может измениться** |
| `EPG_SOURCE_URL` | `https://www.karusel-tv.ru/schedule/Europe/Moscow` | Источник расписания; часовой пояс задаётся в пути |
| `EPG_REFRESH` | `6h` | Как часто обновлять программу |
| `EPG_DAYS` | `12` | Максимум дней вперёд (сайт отдаёт ≈ 10) |
| `PLEX_CLAIM` | пусто | Только для локального стенда |

Память: `vpn` ≈ 40 МиБ, `relay` ≈ 16 МиБ в простое и ≈ 60 МиБ на один поток.

## Разработка

```bash
make check   # gofmt, vet, race-тесты, проверка compose-файлов (без VPN и сети)
```

Правила и карта кода — в [`AGENTS.md`](AGENTS.md), архитектура — в [`docs/architecture.md`](docs/architecture.md), бэклог — в [`TASKS.md`](TASKS.md).

```text
cmd/relay/                HTTP-сервер: тюнер, поток, программа передач
cmd/vpnconf/              Подготовка профиля OpenVPN для Gluetun (init-шаг)
internal/relay/           Конфигурация, HTTP API, HDHomeRun, MediaVitrina, Streamlink
internal/epg/             Расписание, кэш, XMLTV
internal/vpnconf/         base64, резолв remote, атомарная запись профиля
compose.yaml              Сборка из исходников
compose.portainer.yaml    Стек для Portainer с образом из GHCR
compose.local-plex.yaml   Локальный стенд с Plex
.github/workflows/        CI и сборка образа
```

## Ограничения

- Формат плеера и балансера MediaVitrina не документирован и может измениться.
- На каждого зрителя запускается отдельный Streamlink.
- При обрыве в записи будет пауза в несколько секунд. Если источник не восстановился за `RECONNECT_WINDOW`, запись обрывается.

Используйте поток только там, где у вас есть право на просмотр трансляции. Проект не обходит DRM и не имитирует подписку; по умолчанию сервис доступен только с самого хоста.

## Лицензия

[MIT](LICENSE)
