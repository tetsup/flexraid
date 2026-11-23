# flexraid-stress

`flexraid-stress` は、実 Worker の FUSE マウントを通してファイル I/O を実行し、Worker gRPC サーバーの有無・RPC 負荷による影響を比較する Linux 向け統合ベンチマークです。

## 検証条件

同じ FUSE ワークロードを、Worker を毎回停止・再起動して次の3条件で実行します。

| 条件 | Worker gRPC | RPC クライアント |
|---|---|---|
| `off` | 起動しない | なし |
| `on-idle` | 起動・待受 | なし |
| `on-active` | 起動・待受 | 複数クライアントから継続呼び出し |

複数反復では実行順序を交互にし、各条件の前にウォームアップします。各条件は独立した一時ストレージルートを使います。

## FUSE ワークロード

複数のクライアント goroutine が、FUSE マウント上の**同一ファイル**を別々の領域から同時に読み書きします。オフセットは再現可能な擬似ランダム順序です。書き込み直後の読み戻しと、全クライアント停止後の最終読み戻しを行い、各ブロックと SHA-256 を照合します。

同時に各クライアントがディレクトリ・ファイルの作成、rename、stat、unlink、mkdir、rmdir、truncate を実行します。定期的な `fsync` も含みます。

`on-active` では同じ Worker に対して複数の gRPC クライアントを起動し、次の監視 RPC を並行実行します。

- `GetStatus`（複数クライアント）
- `GetDetailedStatus`
- `Inspect`

RPC エラー、FUSE I/O エラー、データ不整合があれば試験は失敗します。

## 計測値

- FUSE 書き込み・読み込みスループット（MiB/s）
- FUSE 書き込み・読み込み・メタデータ IOPS
- FUSE 書き込み・読み込み・メタデータ操作レイテンシ（p50 / p95 / p99）
- gRPC 呼び出し数・エラー数・レイテンシ（平均 / p50 / p95 / p99）
- 最終データ照合結果と SHA-256
- `off` を基準にした `on-idle` / `on-active` のスループット・IOPS変化率

JSON レポートには各反復の生値と条件別中央値を保存します。Markdown 表は標準出力に表示されます。

## 実行要件

- Linux、FUSE 3、`/dev/fuse`、Go toolchain
- Worker が使用する4台以上の専用ストレージルート
- 各ストレージルートは独立したマウントポイントであること
- 各ドライブの WWN が `/dev/disk/by-id/wwn-<WWN>` で解決できること
- 実行ユーザーに FUSE mount とストレージ領域へのアクセス権限があること

試験専用の config YAML を用意します。次は4台のテストドライブを使う例です。

```yaml
disks:
  - root: /mnt/test-disk1/flexraid-data
    mountpoint: /mnt/test-disk1
    wwn: test-drive-1
  - root: /mnt/test-disk2/flexraid-data
    mountpoint: /mnt/test-disk2
    wwn: test-drive-2
  - root: /mnt/test-disk3/flexraid-data
    mountpoint: /mnt/test-disk3
    wwn: test-drive-3
  - root: /mnt/test-disk4/flexraid-data
    mountpoint: /mnt/test-disk4
    wwn: test-drive-4
mountpoint: /mnt/flexraid-stress
grpc_socket_path: /run/flexraid/worker.sock
```

`root` は試験専用の空ディレクトリを指定してください。実データがあるルートを指定しないでください。ベンチマークは各条件用の一時ルートを作成・削除します。

## 実行例

```sh
go build -o bin/flexraid ./cmd/flexraid
go build -o bin/flexraid-stress ./cmd/flexraid-stress

sudo ./bin/flexraid-stress \
  --config ./stress-config.yaml \
  --worker ./bin/flexraid \
  --size 256M \
  --block-size 64K \
  --workers 4 \
  --duration 30s \
  --warmup 3s \
  --rpc-interval 100ms \
  --repetitions 2 \
  --report ./flexraid-stress-report.json
```

主なオプション:

| オプション | 既定値 | 説明 |
|---|---:|---|
| `--size` | 256 MiB | 同一ファイルの共有ワーキングセット |
| `--block-size` | 64 KiB | 1回のランダムオフセット I/O サイズ |
| `--workers` | 4 | 同時 FUSE クライアント数（2以上） |
| `--duration` | 10s | 各条件の計測時間 |
| `--warmup` | 2s | 各条件のウォームアップ時間 |
| `--rpc-interval` | 100ms | 監視 RPC の基本呼び出し間隔 |
| `--repetitions` | 2 | 条件順を交互にした反復数 |
| `--report` | `flexraid-stress-report.json` | JSON レポートの保存先 |

## GitHub Actions

`.github/workflows/concurrency.yml` の `fuse-grpc-integration` job は、4つの一時ファイルを loopback block device として接続し、それぞれに ext4 を作成して実行します。WWN 用 symlink を用意するだけで、Worker、IO、Distribution、Orchestration、FUSE、gRPC は実装そのものを使います。FUSE / Distribution 経路をモックには置き換えません。

この CI は FUSE と gRPC の並行動作・データ整合性、および loopback ストレージ上での相対的な性能差を検証します。実 HDD / SSD の性能、電源制御、実デバイス障害時の挙動を測定するものではありません。実ディスク性能の評価には専用環境を使用してください。
