# Namespace / Persistence Consistency Checklist

このチェックシートは、Orchestration の Namespace 操作と Distribution への永続化が矛盾しないことを確認するための監査表。
「テストが存在する」ことと「整合性契約を満たすこと」は別なので、既存テストがある項目も、失敗注入・再起動後の確認が不足していれば未完了とする。

## 1. 基本契約

### 1.1 Namespace の公開
- [ ] 新規 Object は、Header と必要な全データ Stripe の永続化が成功してから Namespace に公開する。
- [ ] 永続化に失敗した場合、children / objects / headers / root などの公開状態を変更しない。
- [ ] 部分書き込みで物理 Object が残る可能性を定義し、再起動時に誤って Namespace に公開・自動削除しない。
- [ ] 同一 Namespace 操作の実行中に、中間状態を他の操作から観測できないようにする（ロック／直列化の契約を明示する）。

### 1.2 既存 Object の更新
- [ ] 永続化対象 Header の正本を一意にし、runtimeObject.header / runtimeFile.header / headers map の更新順を統一する。
- [ ] 永続化に失敗したとき、メモリ上の Header と Namespace が「旧状態を維持する」のか「dirty な新状態を保持して再試行可能にする」のかを操作ごとに明示する。
- [ ] dirty cache のデータと Header の Size / Layout / ChunkSize が食い違わない。
- [ ] Flush / Fsync の成功・失敗後に、各 Stripe の dirty / clean 状態と永続化済み Header の関係が説明できる。
- [ ] 再起動後の状態が、成功応答した操作の契約と一致する。

### 1.3 削除・複数 Object 更新
- [ ] 論理削除 Header の永続化成功後に Namespace から取り除き、Cleanup キューへ登録する。
- [ ] Cleanup の物理削除成功後にだけ deletionQueue / headers / objects から取り除く。
- [ ] 複数 Header を更新する操作は、各段階の失敗で残る状態と再起動時の復元結果を定義する。
- [ ] 通常エラー時の部分適用と、プロセス停止・電源断時のクラッシュ不整合を区別する。
- [ ] POSIX 操作として原子的であるべき操作（特に Rename の置換）について、部分適用を許容するか、実装で防ぐかを決定する。

## 2. 公開操作一覧とチェック

AGENTS.md の規約どおり、公開操作を commands.go 等へ集約せず、操作ごとのファイルに定義している。以下が現行の主な操作一覧。

| 操作 | 実装 | 永続化とメモリ反映の順序・対象 | 現在のテスト状況 | 監査 |
|---|---|---|---|---|
| CreateRoot | create_root.go | root Header / Stripe を保存後、root・maps を初期化 | 空 FS 作成・再起動復元・非空拒否あり | [ ] 失敗注入・部分 Stripe |
| Create | create.go | 新規 Header / 全 Stripe 保存後、parent.children / objects / headers に公開 | 正常・不正入力・キャンセル・保存失敗あり | [ ] 部分書き込み後の残存状態 |
| Mkdir | mkdir.go | 新規 Directory Header 保存後、親 children と maps に公開 | 正常・不正入力・キャンセル・保存失敗あり | [ ] 部分書き込み後の残存状態 |
| Symlink | symlink.go | リンク先文字列と Header / 全 Stripe 保存後、Namespace に公開 | 複数 Stripe・再起動・不正入力・キャンセルあり | [ ] 部分書き込み失敗 |
| Link | link.go | File Header は変更せず、HardLink Header 保存後に alias を公開 | File の Kind 維持・複数 Link・不正入力あり | [ ] 1段目／2段目の永続化失敗 |
| Unlink(File/Symlink) | unlink.go | Deleted Header 保存後、親 children から除去し Cleanup キューへ | 現状、HardLink 系中心。通常 File / Symlink の失敗系が不足 | [ ] 正常・失敗・再起動 |
| Unlink(File with HardLink references) | unlink.go | HardLink 参照が残る場合は ParentID=nil を保存後、直接 Namespace 名を除去。実体は保持 | 参照が残る File の detachment を確認 | [ ] 保存失敗時の不変性・再起動 |
| Unlink(HardLink) | unlink.go | HardLink Deleted 保存・除去後、残存参照に応じ backing Header を別途更新 | 最終 Link の昇格など一部あり | [ ] 2段目失敗・中断・再起動 |
| Rmdir | rmdir.go | Deleted Header 保存後、親 children から除去・Cleanup 登録 | 空/非空/不正/キャンセルあり | [ ] 永続化失敗・再起動 |
| Rename | rename.go | File は destination HardLink を作成後、source を Unlink。Directory / Symlink は直接移動。置換先があれば先に unlink | HardLink 経由で同じ実体を参照し、元名を unlink する手順を検証 | [ ] Link 成功後の source Unlink 失敗、置換後の再起動 |
| SetAttr | setattr.go | File は runtimeFile Header / dirty cache 更新。handle なしは同期 Flush。Directory/Symlink は Header Stripe 保存 | 属性変更・Size 再構築・キャンセルあり | [ ] Header/Stripe 部分失敗時の状態 |
| Write | write.go | runtimeFile Header と Stripe cache を更新。通常は Flush/Fsync まで永続化しない | 複数 Handle・gap zero-fill・append・再計画などあり | [ ] dirty 状態の明示、Flush 失敗後の再試行 |
| Truncate | truncate.go | サイズ・Layout 再計画と Stripe cache 再構築。永続化は Flush 経由 | サイズ拡張・縮小の一部は SetAttr 経由で検証 | [ ] 公開 Truncate API の直接テスト・失敗系 |
| Flush / Fsync | flush.go | dirty Stripe を StripeID 順（Header の Stripe 0 が先）に保存。成功した Stripe から clean | Write / SetAttr の Flush 経由テストあり | [ ] 中間 Stripe 失敗・Header 先行確定の復元 |
| Cleanup | cleanup.go | Distribution の物理削除成功後に deletionQueue / maps から除去 | 起動時 queue 再構築・削除成功あり | [ ] 物理削除失敗・再試行・途中キャンセル |

## 3. 重点的に追加する失敗注入テスト

### 新規作成系
- [ ] persistNewObject が Stripe 0 保存後、後続 Stripe で失敗した場合、Namespace に Object が公開されない。
- [ ] 再起動後、部分作成 Object が root から到達可能な通常 Object として誤認されない。
- [ ] CreateRoot の root Stripe 保存失敗時、root を持つ Orchestration を返さない。

### Header 更新・データ更新系
- [ ] persistHeaderUpdateLocked の WriteStripe 失敗時、file.header / headers map / runtimeObject.header のどれも成功済み Header と誤認しない。
- [ ] SetAttr（handle なし）の永続化失敗後、メモリ状態と再起動後状態の契約を検証する。
- [ ] Write / Truncate 後の Flush で Stripe 0 成功・Stripe 1 失敗となるケースを作り、Header とデータの復元可能性を検証する。
- [ ] Flush 失敗後、成功済み Stripe は clean、未成功 Stripe は dirty のまま保持され、再 Flush で完了できる。
- [ ] 配置再計画を伴う拡張・縮小の途中失敗で、旧 cache / Header を部分的に置き換えない。

### Namespace の複数 Object 更新系
- [ ] Link: File Header を変更せず、HardLink 保存失敗時の Namespace / 再起動結果。
- [ ] UnlinkHardLink: HardLink の Deleted 保存成功後、backing Header 更新失敗時の Namespace / 再起動結果。
- [ ] Rename: 置換先の削除成功後、source Header 保存失敗時に、通常エラーとしてどの状態を返すか。
- [ ] Rename: Directory の別親移動・空 Directory 置換で、両親の children と ParentID が再起動後も一致する。
- [ ] Cleanup: DeleteObject 失敗・キャンセル時にキューと maps が保持され、次回 Cleanup で再試行できる。

## 4. 現時点で明文化されている非原子性・設計上の注意
- File / HardLink の複数 Header 更新はシリアルであり、複数 Header をまたぐ Atomic 性は要求しない（AGENTS.md）。
- Rename の File は destination HardLink を先に作成し、両方の名前が同じ File 実体を参照した後に source を Unlink する。複数 Header をまたぐクラッシュ原子性はない。Link 後の Unlink が失敗した場合、通常エラーでも両方の名前が残る。置換先がある場合は destination を先に unlink するため、その後の Link 失敗では destination が消えた状態になり得る。
- Write は通常 dirty cache を更新する操作で、Flush / Fsync まで永続化されない。メモリ上で見えることと永続化済みであることを区別する。
- Flush は複数 Stripe を順次保存し、途中失敗時に先行 Stripe の書き込みを取り消さない。現在の契約ではファイル全体の配置移行のクラッシュ原子性は保証されない。
- SetAttr の handle なし経路は同期永続化を試みるが、失敗時に更新済み Header / dirty cache がメモリに残る経路がある。成功時だけ公開する設計にするのか、dirty な新状態を保持して再試行可能にするのかを明確化する。

## 5. 完了条件
各操作について、次をすべて満たしたら監査完了とする。
- [ ] 成功時の永続化内容と Namespace / Runtime の状態が一致する。
- [ ] 永続化失敗時のメモリ状態が契約どおりで、部分更新を誤って成功扱いしない。
- [ ] 再起動後の Namespace が、成功応答した操作の結果と一致する。
- [ ] 複数段階操作の各永続化境界で失敗注入テストがある。
- [ ] dirty cache と Flush 再試行の状態遷移が検証されている。
- [ ] 関連する HardLink、NLink、f_files、deletionQueue の派生状態が一致する。
