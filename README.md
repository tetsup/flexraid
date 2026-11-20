# Flexible RAID

Flexible RAID（FlexRAID）は、複数の通常ファイルシステム上にあるディレクトリをストレージとして利用し、FUSE 経由で単一の階層型ファイルシステムを提供する Linux 向けユーザー空間ファイルシステムです。メディアサーバーなど、複数ドライブにまたがるファイルストレージ用途を想定しています。


## 開発状況

FlexRAID は初期開発段階の実験的なソフトウェアです。基本的なファイル・ディレクトリ操作について pjdfstest による POSIX 互換性テストを実施していますが、すべての Linux / FUSE 機能、障害シナリオ、長時間稼働、性能特性を保証するものではありません。

- **本番環境の唯一のデータ保存先として使用しないでください。**
- RAID の冗長化はバックアップの代替ではありません。重要なデータは別媒体・別システムにもバックアップしてください。
- リリース前のため、永続化形式・API・設定は予告なく変更される可能性があります。既存データとの後方互換性は保証しません。
- 物理ディスクやその上のファイルシステムを直接操作・変更しないでください。FlexRAID が管理する保存領域を手作業で編集すると、データを失う可能性があります。

## 主な特徴

- 複数の独立したディスク上のディレクトリをまとめて一つのファイルシステムとして公開
- 通常のファイルシステム上のファイルを物理保存単位として使用（生ディスクへの直接アクセスは行わない）
- ファイルごとに冗長化・配置方式を持つ設計
- ミラーリングと XOR パリティ付きストライピング
- FUSE による Linux のファイルシステムインターフェース
- 物理ストレージの容量情報と、冗長化を考慮した配置可能容量の集計

## アーキテクチャ

FlexRAID は、外部インターフェースを担う FUSE / Management、データ処理を担う Orchestration / Distribution / I/O、および OS 固有機能を隔離する Platform で構成します。依存関係は次の DAG です。矢印は呼び出し元から呼び出し先への依存を表します。

```mermaid
flowchart TD
    FUSE --> Management
    Management --> Orchestration
    Management --> Distribution
    Orchestration --> Distribution
    Distribution --> IO[I/O]
    Management --> Platform
    Distribution --> Platform
```

- **FUSE** — go-fuse と Management のアダプターです。OS の callback を Management のファイルシステム API に変換し、結果・エラーを FUSE の形式へ写像します。
- **Management** — ファイルシステム操作、管理操作、監視、制御の公開窓口です。システム全体の実行状態と操作受付可否を所有し、起動・初期化・停止を統括します。各操作は状態による事前判定、必要なロック取得、ロック取得後の状態再確認を経て実行します。Orchestration と Distribution の統括、Object 単位の排他制御、Cleanup / Recovery 等の管理タスク調整、操作メトリクスの集計も担当します。
- **Orchestration** — Header、論理 Object、Namespace、ファイル offset、ストライプ geometry、配置方針を管理します。通常のファイル操作では Distribution を直接利用し、論理ファイルの意味を保ったままストライプ単位の要求へ変換します。
- **Distribution** — 論理ストライプをチャンクへ分割し、ミラー / パリティを生成し、Version・物理配置・復旧を管理します。同一 ObjectID + StripeID の競合を防ぐ Stripe 単位の排他制御も所有します。
- **I/O** — 指定されたストレージディレクトリ上で物理ファイルの読み書き・同期・列挙・削除を行います。Go の標準 API を中心とした基本操作を担当し、Header やストライプの意味、配置・復旧方針、ディスク電源制御は扱いません。
- **Platform** — OS・実行環境ごとの差異を吸収する独立コンポーネントです。ファイルシステム容量、物理ドライブの識別・情報、電源状態の取得、スピンアップ / スピンダウン要求など、OS 固有 API を必要とする機能を提供します。状態に基づく判断やスケジューリングは Management が担当します。

### 操作経路と責務境界

通常の FUSE ファイル操作は `FUSE -> Management -> Orchestration -> Distribution -> I/O` の経路を通ります。Platform は I/O と同列の独立コンポーネントで、Management や Distribution など必要な上位コンポーネントから利用します。I/O と Platform は相互に依存しません。Management は Orchestration と Distribution の両方を直接利用できる統括レイヤーですが、Orchestration と Distribution の親子関係は維持します。Management から Distribution への直接呼び出しは、論理ファイル操作を必要としない Cleanup の物理処理や監視情報の取得などに限定し、通常のファイルデータ操作を Orchestration 経由から迂回させません。

Management が提供する外部 API は次の領域を想定します。

- **Filesystem API** — FUSE が必要とする Lookup / Getattr / Read / Write / Create / Rename 等の論理ファイルシステム操作。
- **Administration API** — Cleanup / Recovery / Scrub 等の管理タスクの開始・進捗確認・キャンセル。
- **Monitoring API** — 操作回数・所要時間・結果、ドライブ別 I/O エラー数などの状態・統計 snapshot の取得。
- **Control API** — 管理タスクの pause / resume や実行制御。個々の物理 I/O の意味解釈は行わない。

通常のファイル操作は結果・エラーを同期的に返します。長時間の管理タスクは、呼び出し元を物理削除や復旧の完了まで拘束せず、有限単位の処理と進捗を扱える API とします。

### 排他制御と Cleanup

システム全体の実行状態と操作受付可否は Management が所有します。各操作は下位レイヤーを呼び出す前に現在の状態で受付可能か判断し、必要な Object / メンテナンスロックを取得した後にも状態と操作前提を再確認します。ロック待機中の状態変更や、状態遷移と処理開始の競合を防ぐため、Management は受付済み操作の admission と状態遷移を同期します。状態遷移では新規受付を止め、遷移先の契約に応じて進行中操作の完了またはキャンセルを待ちます。
 
Namespace / Header / Runtime の論理状態は Orchestration が所有し、Management が必要な Object ロックを取得したうえで Orchestration API を呼び出します。異なる Object の操作を全体 mutex で一律に直列化せず、親 Directory や対象 Object など操作に必要なロック範囲で並行性を保ちます。

Distribution はストライプ状態を所有し、同一 ObjectID + StripeID の Read / Write / Recovery / Cleanup を直列化する Stripe ロックを所有します。Management / Orchestration はこの内部ロックを直接操作しません。I/O は物理ファイル操作に専念し、上位層の Object / Stripe ロックを管理しません。

削除は論理削除と物理削除を分離します。Orchestration は Header の Deleted 状態を永続化し、起動時の整合性検査を通過した削除済み ObjectID の候補一覧を Management へ返します。Management はこの永続状態から再構築可能な一時的削除待ちキューを所有し、Cleanup の実行タイミング・実行量を制御します。Distribution は旧 Version・孤立チャンク・論理削除 Object の Stripe / Chunk 単位の物理削除候補と処理結果を管理し、有限単位で物理削除します。Object 全体の物理削除では Stripe 0 / Header を最後に処理し、全 Stripe の削除完了後に Management が Orchestration へ Object のメモリ状態の確定を依頼します。

Management と Platform を含む構成は今後の目標アーキテクチャです。現行実装では FUSE が Orchestration を直接呼び出し、Orchestration が mutex と削除待ちキューを所有しており、システム状態による受付制御も Management には集約されていません。また、現行の `io.GetCapacity` は `unix.Statfs` を使用しているため、OS 固有の環境情報取得を Platform へ分離する作業も今後の移行対象です。Management 導入時は、排他制御・削除待ちキュー・システム状態と操作受付判断・初期化ライフサイクルを段階的に移行します。Orchestration のロックやキューを単に削除して整合性を失わせず、永続 Header の Deleted 状態から Management のキューを再構築できるようにします。

## 初期実装のデータ配置

- ストレージディレクトリは最低4つ必要です。5つ以上も指定でき、台数に上限はありません。新しいストライプの配置では、必要なチャンク数の I/O を `AvailableBytes` の多い順に選択し、余分なディレクトリは配置候補として待機します。
- Mirror は複数コピーを保存します（現在の容量計算・既定構成では 2 コピー）。
- XOR Parity は既定で 3 データチャンク + 1 パリティチャンクを使用します。データチャンク数・パリティ数は現行 CLI の YAML から変更できません。
- XOR パリティで復元できるのは、1 ストライプ内のデータチャンク 1 個の欠損までです。
- 配置済みデータの読み込みでは、現在の設定ではなく保存済みの配置情報を使用します。
- Stripe 0 の先頭 1 KiB は Header 用に予約されます。Stripe 0 の物理チャンクサイズは 4 KiB 固定で、後続ストライプのチャンクサイズは Header に記録されます。
- ファイルサイズ上限は 16 TiB、名前長の上限は 255 bytes です。

復旧はチャンクの欠落または物理読み取りエラーを対象とします。読み取れたデータ同士の比較やパリティ不一致から、正しい内容を推測・訂正する機能はありません。サイレントデータ破損や改ざんの検出を保証しません。

## FUSE / POSIX の対応状況

以下は FlexRAID の FUSE アダプターが実装している操作の範囲です。Linux カーネル、FUSE、マウント環境によって利用者から見える挙動が異なる場合があります。

### 対応している操作

- パス検索・属性取得・ディレクトリ一覧（Lookup / Getattr / Readdir）
- 通常ファイルの Open / Read / Write
- Create、Mknod（通常ファイルのみ）
- Mkdir、Rmdir
- Unlink、Rename、Hard Link、Symbolic Link、Readlink
- Setattr（mode、uid、gid、size、atime、mtime）
- Flush、Fsync、Release
- Statfs

権限判定は FUSE の `default_permissions` を有効にし、基本的にカーネル側へ委ねます。FUSE mount は `allow_other` を指定します。

### 非対応・制限事項

- **FIFO / named pipe** — 作成非対応。Mknod / mkfifo は `EOPNOTSUPP`。
- **キャラクタデバイス / ブロックデバイス** — 作成非対応。Mknod は `EOPNOTSUPP`。
- **UNIX domain socket** — 作成非対応。
- **Tmpfile（名前を持たない一時ファイル）** — 非対応。
- **拡張属性（xattr）** — get / set / list / remove の各操作は未実装。
- **Fallocate** — 事前領域確保 API は未実装。
- **Lseek の SEEK_DATA / SEEK_HOLE** — 専用 callback は未実装。
- **POSIX advisory locks** — FUSE の Getlk / Setlk / Setlkw は未実装で、マウント時にも有効化していません。

Mknod は通常ファイル（`S_IFREG`）だけを作成します。ファイル種別が指定されていない場合は `EINVAL`、特殊ファイル種別は `EOPNOTSUPP` を返します。特殊ファイルを通常ファイルとして代用することはありません。

上記以外の FUSE / Linux 機能についても、実装・テスト済みと明記されていないものはサポートを保証しません。

## 必要環境

- Linux
- Go 1.25 以上（ソースからビルドする場合）
- FUSE 3 のユーザー空間ツール（例: `fusermount3`）および利用可能な FUSE デバイス（通常は `/dev/fuse`）
- ストレージ用ディレクトリを作成・読み書きできる権限
- マウントポイントを作成・マウントできる権限

## ビルド

リポジトリのルートで実行します。

```sh
go build -o flexraid ./main
```

## 設定

Management が起動時に YAML 設定を読み込みます。ストレージは **4つ以上** 指定してください。各 disk の `root`・物理ファイルシステムの `mountpoint`・`wwn` は必須の組で、各値は設定内で一意にしてください。FlexRAID 全体の `mountpoint` は各物理ディスクの mountpoint と別に指定します。データチャンク数・パリティ数・最大チャンクサイズを指定する YAML 項目はなく、現行 CLI は既定の 3 データチャンク + 1 パリティ構成を使用します。

```yaml
disks:
  - root: /srv/flexraid/disk1/data
    mountpoint: /srv/flexraid/disk1
    wwn: 0x5000000000000001
    cleanup_delete_allowed: false
    cleanup_preserve_allowed: false
  - root: /srv/flexraid/disk2/data
    mountpoint: /srv/flexraid/disk2
    wwn: 0x5000000000000002
  - root: /srv/flexraid/disk3/data
    mountpoint: /srv/flexraid/disk3
    wwn: 0x5000000000000003
  - root: /srv/flexraid/disk4/data
    mountpoint: /srv/flexraid/disk4
    wwn: 0x5000000000000004
mountpoint: /mnt/flexraid
```

各 `disks` エントリーでは FlexRAID の保存ディレクトリ `root`、物理ディスク上のファイルシステム `mountpoint`、物理ドライブの `wwn` を必ず一組で指定します。WWN は Linux の `/dev/disk/by-id/wwn-` に続く値です。起動時に全 WWN を物理ブロックデバイスとして解決できない場合は、ストレージディレクトリを作成する前に起動を失敗させます。

各ドライブでは `cleanup_delete_allowed` と `cleanup_preserve_allowed` を独立して指定できます。どちらも省略時は `false`（禁止）です。前者は旧 Version の物理削除、後者は異常 Version の保全領域への退避を許可します。稼働中の変更は Management の `SetDriveCleanupDeletePermission` / `SetDriveCleanupPreservePermission` API で行います。これらの設定・API は許可状態だけを変更し、それ自体で Cleanup を開始しません。実行タイミングと対象の選択は Management のスケジューリング／手動実行フローが制御します。

起動時に Management が各 WWN を Linux の物理ブロックデバイスへ解決できることを確認します。解決に失敗した場合は、ストレージディレクトリを作成する前に起動を中断します。ストレージ `root` と FlexRAID 全体の mountpoint が存在しない場合は必要なディレクトリを作成します。既存のストレージ `root` に FlexRAID のデータがある場合は、その Namespace を読み込んで再マウントします。空の場合は root Directory を初期化します。新しいストライプの配置では、必要なチャンク数の I/O を `AvailableBytes` の多い順に選択します。既定の XOR Parity は4チャンクを必要とするため、5つ以上のディレクトリを指定しても1ストライプに使うのは4つで、残りは空き容量の変化などに応じた後続の配置候補になります。Mirror は2コピーを保存するため、配置時に空き容量の多い2ディレクトリを選択します。

## 起動・アンマウント

```sh
./flexraid -config /etc/flexraid.yaml
```

アンマウントは別のターミナルから行います。

```sh
fusermount3 -u /mnt/flexraid
```

マウント中は FlexRAID のプロセスを終了させず、先に正常にアンマウントしてください。

## データと整合性に関する注意

- 各ストレージディレクトリ内の物理ファイルは FlexRAID が管理します。名前変更・削除・編集をしないでください。
- ストライプ単位で新しいデータバージョンを書き込み、対象ストライプの必要なチャンク書き込みが完了してから成功を返します。書き込み途中で失敗した Version は実行中の最新有効 Version に切り替えませんが、書き込み済みチャンクは物理的に残る場合があります。
- 起動時は各ストライプの Version を個別に検証し、復元可能なもののうち最も新しい Version を選択します。したがって、以前に書き込み失敗した Version でも残存チャンクだけで復元可能なら、再起動後に選択される場合があります。新しい Version が復元不能なら、以前の復元可能 Version にフォールバックします。
- Mirror は複製が1つ以上あれば復元できます。XOR 1 パリティはデータチャンク1つの欠損まで復元できます。2つ以上のデータチャンクが欠損した場合は復元できません。
- これは複数ストライプ・複数 Namespace Object にまたがるトランザクションを意味しません。複数 Header を更新する操作のクラッシュ時原子性は保証されません。
- ディスク故障や読み取りエラーからの復旧能力は、選択された配置方式と残存チャンクに依存します。冗長性を超える障害ではデータを復旧できません。
- RAID は誤削除、ランサムウェア、ホスト故障、火災などからデータを保護するバックアップではありません。

## 開発者向けテスト

Go のテストは次のコマンドで実行します。

```sh
go test ./...
```

POSIX の基本操作は pjdfstest を用いて検証します。FlexRAID v1 でサポートしない特殊ファイルなどを前提とする一部ケースは、リポジトリ内の `tools/pjdfstest-flexraid-skiplist.tsv` に理由を記録したうえで SKIP とします。実装済み機能の不具合を SKIP で隠すことは認めません。

詳細な実装規約は `AGENTS.md` を参照してください。

## 管理コマンドとプロセス構成（設計方針）

外部からの管理操作は `frcon` を入口とし、Controller と論理ドライブごとの Worker（実行ファイル名 `flexraid`）がローカル RPC で連携する構成を目標とします。プロセス間通信には Unix Domain Socket 上の gRPC、メッセージ形式には Protocol Buffers を使用します。

- **Controller** は外部コマンドを受け付け、対象 Worker へ操作を中継します。常駐 RPC サーバーモード（`frcon serve`）と、単発コマンドを実行して終了するモードを持ちます。
- **Worker**（実行ファイル名 `flexraid`）は論理ドライブごとに一つ起動する独立プロセスです。各 Worker が担当ドライブの Management と下位レイヤーの状態を所有し、複数 Worker は互いに独立して動作します。Worker は Controller を介さず単独起動することもできます。
- Controller は Worker を起動できますが、Worker の親・監督プロセスにはなりません。Controller が終了しても Worker は動作を継続し、Worker は Controller を介さず単独起動することもできます。
- Controller の停止は Worker の停止を意味しません。Worker の停止は対象ドライブを指定した明示的な操作で行います。
- Controller 再起動後も既存 Worker を再発見して接続できる構成とします。

### 統合設定ファイルと読み込み規則

Controller と全 Worker の設定は、単一の YAML 設定ファイルにまとめます。トップレベルに `controller` と `workers` を定義し、各 Worker は `name` で識別します。Worker 名は設定ファイル内で一意でなければなりません。

```yaml
version: 1

controller:
  socket_path: /run/flexraid/controller.sock
  log_level: info

workers:
  - name: main
    socket_path: /run/flexraid/workers/main.sock
    devices:
      - uuid: "..."
      - uuid: "..."
      - uuid: "..."
      - uuid: "..."
    management:
      cleanup:
        execution: when_spun_up
        policy:
          delete_deprecated_versions_allowed: true
          preserve_abnormal_data_allowed: false
          delete_logical_objects_allowed: false

  - name: backup
    socket_path: /run/flexraid/workers/backup.sock
    devices:
      - uuid: "..."
      - uuid: "..."
      - uuid: "..."
      - uuid: "..."
```

設定ファイルの選択規則は Controller / Worker で共通とし、次の優先順位で決定します。

1. `--config <path>` が指定されている場合は、そのファイルを使用します。
2. `--config` が指定されていない場合は、まず `/etc/flexraid/config.yaml` を確認します。
3. そこにファイルが存在しない場合は、カレントディレクトリの `./config.yaml` を使用します。
4. どちらも存在しない場合は起動エラーにします。

`--config` で明示されたファイルが存在しない、または読み込めない場合はエラーとし、既定パスへフォールバックしません。相対パスはカレントディレクトリを基準に解決します。

Controller は統合設定ファイルから Controller 自身の設定と Worker 一覧を読み込みます。Worker を単独起動する場合は、起動引数で `--name <worker-name>` を必須指定し、同じ設定ファイル内から名前が一致する Worker の設定を一つだけ選択して起動します。一致する Worker がない場合や名前が重複している場合は起動エラーにします。

```sh
# 設定ファイルを自動選択して Controller を起動
frcon serve

# 設定ファイルを明示して Controller を起動
frcon serve --config ./production.yaml

# Worker を単独起動（自動選択された設定ファイルから main を選択）
flexraid --name main

# Worker を単独起動（設定ファイルを明示）
flexraid --config ./production.yaml --name main
```

### 管理コマンドの整備対象

以下は今後整備する `frcon` のコマンドの種類です。引数・出力形式は実装時に確定します。

```text
frcon
├── serve
├── system
│   ├── status
│   ├── start
│   └── stop
├── drive
│   ├── list
│   ├── status <drive_name> [--detail]
│   ├── inspect <drive_name>
│   ├── start <drive_name>
│   └── stop <drive_name>
├── cleanup
│   ├── status <drive_name>
│   ├── policy
│   │   ├── show <drive_name>
│   │   └── set <drive_name> ...
│   └── run <drive_name>
└── config
    ├── show
    ├── validate
    └── reload
```

- `drive status <drive_name>` は簡易ヘルスチェックです。論理ドライブ全体として問題がないかを短時間で確認します。
- `drive status <drive_name> --detail` は運用監視用の詳細状態です。全体集計に加えて、物理ドライブごとの生死・接続・電源状態、容量、I/O 統計、整合性情報を分けて返します。
- `drive inspect <drive_name>` は監査用の完全な情報取得です。構成、Namespace、Stripe / Version / Chunk、Cleanup 対象とサイズ、現在および Cleanup 後の論理空き容量、履歴・統計などを網羅します。必要に応じてストレージ走査を伴います。
- 起動後に変化しない物理デバイス情報は Initialization 中に取得し、Ready になる前に snapshot として確定します。取得にデバイスの起動が必要な情報は Initialization の SpinUp 処理と連携し、非侵襲的に取得できる情報のために不要な SpinUp は行いません。稼働中に変化する情報は実際の操作・I/O・SpinUp / SpinDown に伴って更新します。status の照会だけを理由に Standby 中のデバイスを起こしません。取得元・取得時刻・取得状態を保持し、取得不能・未取得・古い snapshot を区別します。
- Cleanup Schedule は `drive status --detail` の Cleanup 情報内に、状態・Policy などと同列の項目として表示し、`drive inspect` にも監査情報として含めます。独立した公開関数・RPC・CLI コマンドは設けません。
- `system start/stop` は Controller の常駐 RPC サーバーを対象とし、Worker 群を一括起動・停止する意味ではありません。
- `drive start/stop` は指定した論理ドライブの Worker だけを対象とします。
- Cleanup の policy / run は、指定した論理ドライブの Worker に対する操作です。

実装規約・プロセス分離・RPC 契約の詳細は `AGENTS.md` を参照してください。
