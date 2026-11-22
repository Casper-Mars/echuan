# issue #122 断言结果（after）

## 复跑

- 脚本（随卡交付，**不进 `make web` 门禁**）：`shots/issue122/run.mjs` + `shots/issue122/lib/{fixtures,stub}.mjs`；
  参数 `--dist`（被测产物）/ `--out`（截图与结果目录）/ `--mode` / `--expect` / `--clips`（出图档位，
  缺省 `1440x900,768x1024,390x844`；**断言条数随档位数变**，下表 215 条对应 `1440x900,390x844`）；`--cdp` 缺省
  `ws://browserless:3000?token=test-token`；页面地址 `http://<--host>:<--port>/`（默认 `dsh-agent`，
  服务在宿主时改用 `host.docker.internal`）；puppeteer-core 不进仓库依赖，用
  `--puppeteer-dir` / `PUPPETEER_DIR` / `NODE_PATH` 指定按哪个目录解析。
- 改后复跑：`node shots/issue122/run.mjs --dist internal/web/dist --out <out> --mode after --expect pass --clips 1440x900,390x844`
  → 实测 **exit 0，215/215 通过**，截图 77 张写入 `<out>/<page>/<viewport>-<state>.png`。
- 改前复跑：`node shots/issue122/run.mjs --dist <基线 8be9b81 上 make web 的产物> --out <out> --mode before --expect any --clips 1440x900,390x844`
  → 实测 **exit 0，156/215 通过**（59 项失败 = 缺陷面：内距 0 / 文本贴边 / 状态条压字 / 长串溢出 1778px / 焦点环被裁 / 折叠列）。
- 仓库内发布的是两次运行的子集：改后见 `shots/issue122/<page>/`，改前同名文件在 `shots/issue122/before/<page>/`。
  下表是本文件对应这一次运行的完整断言明细。

- dist: `/workspace/wt-t_01M81W481ESJGYM9BGF3NMVWYS/internal/web/dist`
- base_url: `http://dsh-agent:8931`
- generated_at: 2026-11-22T15:08:27.249Z
- 合计：215/215 通过，0 失败；截图 77 张

| 断言 | 结果 | 实际值 |
|---|---|---|
| board/1440x900/card.heights | PASS | min=91.95px max=139.05px n=16 |
| board/1440x900/extract.longtitle | PASS | captured |
| board/1440x900/extract.longtoken | PASS | captured |
| board/1440x900/extract.longid | PASS | captured |
| board/1440x900/extract.multibadge | PASS | captured |
| board/1440x900/cards>=1 | PASS | 16 card(s) |
| board/1440x900/AC1.padding=8 | PASS | all 16 cards 8/8/8/8 |
| board/1440x900/AC1.textGap>=8 | PASS | min gap ok on 16 cards |
| board/1440x900/AC1.statusBarOnGlyph | PASS | bar 3px 未压字（16 卡） |
| board/1440x900/AC2.longTitle.lines<=3 | PASS | lines=3.01 lineHeight=23.55 |
| board/1440x900/AC2.longTitle.clamped | PASS | scrollHeight>clientHeight=true |
| board/1440x900/AC2.longTitle.noOverflowX | PASS | scrollWidth-clientWidth=0 |
| board/1440x900/AC3.longToken.lines<=3 | PASS | lines=3.01 |
| board/1440x900/AC3.longToken.noOverflowX | PASS | scrollWidth-clientWidth=0 |
| board/1440x900/AC3.longToken.noDescendantOverflow | PASS | 越出 padding box 的后代=0 |
| board/1440x900/innerOverflow=none | PASS | no descendant outside padding box |
| board/1440x900/AC6.focusRing | PASS | outline solid 2px offset -2px（外沿相对命中区 0px，≤0 才不被裁）; hit⊂card=true |
| board/1440x900/consoleErrors | PASS | none |
| board/768x1024/card.heights | PASS | min=91.95px max=139.05px n=16 |
| board/768x1024/cards>=1 | PASS | 16 card(s) |
| board/768x1024/AC1.padding=8 | PASS | all 16 cards 8/8/8/8 |
| board/768x1024/AC1.textGap>=8 | PASS | min gap ok on 16 cards |
| board/768x1024/AC1.statusBarOnGlyph | PASS | bar 3px 未压字（16 卡） |
| board/768x1024/AC2.longTitle.lines<=3 | PASS | lines=3.01 lineHeight=23.55 |
| board/768x1024/AC2.longTitle.clamped | PASS | scrollHeight>clientHeight=true |
| board/768x1024/AC2.longTitle.noOverflowX | PASS | scrollWidth-clientWidth=0 |
| board/768x1024/AC3.longToken.lines<=3 | PASS | lines=3.01 |
| board/768x1024/AC3.longToken.noOverflowX | PASS | scrollWidth-clientWidth=0 |
| board/768x1024/AC3.longToken.noDescendantOverflow | PASS | 越出 padding box 的后代=0 |
| board/768x1024/innerOverflow=none | PASS | no descendant outside padding box |
| board/768x1024/AC6.focusRing | PASS | outline solid 2px offset -2px（外沿相对命中区 0px，≤0 才不被裁）; hit⊂card=true |
| board/768x1024/consoleErrors | PASS | none |
| board/390x844/card.heights | PASS | min=91.95px max=139.05px n=16 |
| board/390x844/extract.longtitle | PASS | captured |
| board/390x844/extract.longtoken | PASS | captured |
| board/390x844/extract.longid | PASS | captured |
| board/390x844/extract.multibadge | PASS | captured |
| board/390x844/cards>=1 | PASS | 16 card(s) |
| board/390x844/AC1.padding=8 | PASS | all 16 cards 8/8/8/8 |
| board/390x844/AC1.textGap>=8 | PASS | min gap ok on 16 cards |
| board/390x844/AC1.statusBarOnGlyph | PASS | bar 3px 未压字（16 卡） |
| board/390x844/AC2.longTitle.lines<=3 | PASS | lines=3.01 lineHeight=23.55 |
| board/390x844/AC2.longTitle.clamped | PASS | scrollHeight>clientHeight=true |
| board/390x844/AC2.longTitle.noOverflowX | PASS | scrollWidth-clientWidth=0 |
| board/390x844/AC3.longToken.lines<=3 | PASS | lines=3.01 |
| board/390x844/AC3.longToken.noOverflowX | PASS | scrollWidth-clientWidth=0 |
| board/390x844/AC3.longToken.noDescendantOverflow | PASS | 越出 padding box 的后代=0 |
| board/390x844/AC4.cardFitsContainer | PASS | all cards <= parent width |
| board/390x844/AC4.metaNotClipped | PASS | meta items 无裁剪 |
| board/390x844/AC4.taskIdSingleLine | PASS | task_id 均单行 |
| board/390x844/AC4.taskIdTitleAttr | PASS | title 保留完整 task_id |
| board/390x844/innerOverflow=none | PASS | no descendant outside padding box |
| board/390x844/AC6.focusRing | PASS | outline solid 2px offset -2px（外沿相对命中区 0px，≤0 才不被裁）; hit⊂card=true |
| board/390x844/consoleErrors | PASS | none |
| inbox/1440x900/card.heights | PASS | min=115.5px max=139.05px n=10 |
| inbox/1440x900/extract.longtitle | PASS | captured |
| inbox/1440x900/extract.longtoken | PASS | captured |
| inbox/1440x900/extract.longid | PASS | captured |
| inbox/1440x900/extract.multibadge | PASS | captured |
| inbox/1440x900/cards>=1 | PASS | 10 card(s) |
| inbox/1440x900/AC1.padding=8 | PASS | all 10 cards 8/8/8/8 |
| inbox/1440x900/AC1.textGap>=8 | PASS | min gap ok on 10 cards |
| inbox/1440x900/AC1.statusBarOnGlyph | PASS | bar 3px 未压字（10 卡） |
| inbox/1440x900/AC2.longTitle.lines<=3 | PASS | lines=3.01 lineHeight=23.55 |
| inbox/1440x900/AC2.longTitle.clamped | PASS | scrollHeight>clientHeight=true |
| inbox/1440x900/AC2.longTitle.noOverflowX | PASS | scrollWidth-clientWidth=0 |
| inbox/1440x900/AC3.longToken.lines<=3 | PASS | lines=3.01 |
| inbox/1440x900/AC3.longToken.noOverflowX | PASS | scrollWidth-clientWidth=0 |
| inbox/1440x900/AC3.longToken.noDescendantOverflow | PASS | 越出 padding box 的后代=0 |
| inbox/1440x900/innerOverflow=none | PASS | no descendant outside padding box |
| inbox/1440x900/AC6.focusRing | PASS | outline solid 2px offset -2px（外沿相对命中区 0px，≤0 才不被裁）; hit⊂card=true |
| inbox/1440x900/consoleErrors | PASS | none |
| inbox/768x1024/card.heights | PASS | min=91.95px max=139.05px n=10 |
| inbox/768x1024/cards>=1 | PASS | 10 card(s) |
| inbox/768x1024/AC1.padding=8 | PASS | all 10 cards 8/8/8/8 |
| inbox/768x1024/AC1.textGap>=8 | PASS | min gap ok on 10 cards |
| inbox/768x1024/AC1.statusBarOnGlyph | PASS | bar 3px 未压字（10 卡） |
| inbox/768x1024/AC2.longTitle.lines<=3 | PASS | lines=3.01 lineHeight=23.55 |
| inbox/768x1024/AC2.longTitle.clamped | PASS | scrollHeight>clientHeight=true |
| inbox/768x1024/AC2.longTitle.noOverflowX | PASS | scrollWidth-clientWidth=0 |
| inbox/768x1024/AC3.longToken.lines<=3 | PASS | lines=3.01 |
| inbox/768x1024/AC3.longToken.noOverflowX | PASS | scrollWidth-clientWidth=0 |
| inbox/768x1024/AC3.longToken.noDescendantOverflow | PASS | 越出 padding box 的后代=0 |
| inbox/768x1024/innerOverflow=none | PASS | no descendant outside padding box |
| inbox/768x1024/AC6.focusRing | PASS | outline solid 2px offset -2px（外沿相对命中区 0px，≤0 才不被裁）; hit⊂card=true |
| inbox/768x1024/consoleErrors | PASS | none |
| inbox/390x844/card.heights | PASS | min=91.95px max=139.05px n=10 |
| inbox/390x844/extract.longtitle | PASS | captured |
| inbox/390x844/extract.longtoken | PASS | captured |
| inbox/390x844/extract.longid | PASS | captured |
| inbox/390x844/extract.multibadge | PASS | captured |
| inbox/390x844/cards>=1 | PASS | 10 card(s) |
| inbox/390x844/AC1.padding=8 | PASS | all 10 cards 8/8/8/8 |
| inbox/390x844/AC1.textGap>=8 | PASS | min gap ok on 10 cards |
| inbox/390x844/AC1.statusBarOnGlyph | PASS | bar 3px 未压字（10 卡） |
| inbox/390x844/AC2.longTitle.lines<=3 | PASS | lines=3.01 lineHeight=23.55 |
| inbox/390x844/AC2.longTitle.clamped | PASS | scrollHeight>clientHeight=true |
| inbox/390x844/AC2.longTitle.noOverflowX | PASS | scrollWidth-clientWidth=0 |
| inbox/390x844/AC3.longToken.lines<=3 | PASS | lines=3.01 |
| inbox/390x844/AC3.longToken.noOverflowX | PASS | scrollWidth-clientWidth=0 |
| inbox/390x844/AC3.longToken.noDescendantOverflow | PASS | 越出 padding box 的后代=0 |
| inbox/390x844/AC4.cardFitsContainer | PASS | all cards <= parent width |
| inbox/390x844/AC4.metaNotClipped | PASS | meta items 无裁剪 |
| inbox/390x844/AC4.taskIdSingleLine | PASS | task_id 均单行 |
| inbox/390x844/AC4.taskIdTitleAttr | PASS | title 保留完整 task_id |
| inbox/390x844/innerOverflow=none | PASS | no descendant outside padding box |
| inbox/390x844/AC6.focusRing | PASS | outline solid 2px offset -2px（外沿相对命中区 0px，≤0 才不被裁）; hit⊂card=true |
| inbox/390x844/consoleErrors | PASS | none |
| queue/1440x900/card.heights | PASS | min=91.95px max=139.05px n=6 |
| queue/1440x900/extract.longtitle | PASS | captured |
| queue/1440x900/extract.longtoken | PASS | captured |
| queue/1440x900/extract.longid | PASS | captured |
| queue/1440x900/extract.multibadge | PASS | captured |
| queue/1440x900/cards>=1 | PASS | 6 card(s) |
| queue/1440x900/AC1.padding=8 | PASS | all 6 cards 8/8/8/8 |
| queue/1440x900/AC1.textGap>=8 | PASS | min gap ok on 6 cards |
| queue/1440x900/AC1.statusBarOnGlyph | PASS | bar 3px 未压字（6 卡） |
| queue/1440x900/AC2.longTitle.lines<=3 | PASS | lines=3.01 lineHeight=23.55 |
| queue/1440x900/AC2.longTitle.clamped | PASS | scrollHeight>clientHeight=true |
| queue/1440x900/AC2.longTitle.noOverflowX | PASS | scrollWidth-clientWidth=0 |
| queue/1440x900/AC3.longToken.lines<=3 | PASS | lines=3.01 |
| queue/1440x900/AC3.longToken.noOverflowX | PASS | scrollWidth-clientWidth=0 |
| queue/1440x900/AC3.longToken.noDescendantOverflow | PASS | 越出 padding box 的后代=0 |
| queue/1440x900/innerOverflow=none | PASS | no descendant outside padding box |
| queue/1440x900/AC6.focusRing | PASS | outline solid 2px offset -2px（外沿相对命中区 0px，≤0 才不被裁）; hit⊂card=true |
| queue/1440x900/consoleErrors | PASS | none |
| queue/768x1024/card.heights | PASS | min=91.95px max=139.05px n=6 |
| queue/768x1024/cards>=1 | PASS | 6 card(s) |
| queue/768x1024/AC1.padding=8 | PASS | all 6 cards 8/8/8/8 |
| queue/768x1024/AC1.textGap>=8 | PASS | min gap ok on 6 cards |
| queue/768x1024/AC1.statusBarOnGlyph | PASS | bar 3px 未压字（6 卡） |
| queue/768x1024/AC2.longTitle.lines<=3 | PASS | lines=3.01 lineHeight=23.55 |
| queue/768x1024/AC2.longTitle.clamped | PASS | scrollHeight>clientHeight=true |
| queue/768x1024/AC2.longTitle.noOverflowX | PASS | scrollWidth-clientWidth=0 |
| queue/768x1024/AC3.longToken.lines<=3 | PASS | lines=3.01 |
| queue/768x1024/AC3.longToken.noOverflowX | PASS | scrollWidth-clientWidth=0 |
| queue/768x1024/AC3.longToken.noDescendantOverflow | PASS | 越出 padding box 的后代=0 |
| queue/768x1024/innerOverflow=none | PASS | no descendant outside padding box |
| queue/768x1024/AC6.focusRing | PASS | outline solid 2px offset -2px（外沿相对命中区 0px，≤0 才不被裁）; hit⊂card=true |
| queue/768x1024/consoleErrors | PASS | none |
| queue/390x844/card.heights | PASS | min=91.95px max=139.05px n=6 |
| queue/390x844/extract.longtitle | PASS | captured |
| queue/390x844/extract.longtoken | PASS | captured |
| queue/390x844/extract.longid | PASS | captured |
| queue/390x844/extract.multibadge | PASS | captured |
| queue/390x844/cards>=1 | PASS | 6 card(s) |
| queue/390x844/AC1.padding=8 | PASS | all 6 cards 8/8/8/8 |
| queue/390x844/AC1.textGap>=8 | PASS | min gap ok on 6 cards |
| queue/390x844/AC1.statusBarOnGlyph | PASS | bar 3px 未压字（6 卡） |
| queue/390x844/AC2.longTitle.lines<=3 | PASS | lines=3.01 lineHeight=23.55 |
| queue/390x844/AC2.longTitle.clamped | PASS | scrollHeight>clientHeight=true |
| queue/390x844/AC2.longTitle.noOverflowX | PASS | scrollWidth-clientWidth=0 |
| queue/390x844/AC3.longToken.lines<=3 | PASS | lines=3.01 |
| queue/390x844/AC3.longToken.noOverflowX | PASS | scrollWidth-clientWidth=0 |
| queue/390x844/AC3.longToken.noDescendantOverflow | PASS | 越出 padding box 的后代=0 |
| queue/390x844/AC4.cardFitsContainer | PASS | all cards <= parent width |
| queue/390x844/AC4.metaNotClipped | PASS | meta items 无裁剪 |
| queue/390x844/AC4.taskIdSingleLine | PASS | task_id 均单行 |
| queue/390x844/AC4.taskIdTitleAttr | PASS | title 保留完整 task_id |
| queue/390x844/innerOverflow=none | PASS | no descendant outside padding box |
| queue/390x844/AC6.focusRing | PASS | outline solid 2px offset -2px（外沿相对命中区 0px，≤0 才不被裁）; hit⊂card=true |
| queue/390x844/consoleErrors | PASS | none |
| board/1440x900/collapsed.expand | PASS | expanded card pad block/inline-start 8/8 |
| board/390x844/collapsed.expand | PASS | expanded card pad block/inline-start 8/8 |
| board/1440x900/loading.skeleton | PASS | 14 skeleton(s), heights=42/42/42/42/42/42/42/42/42/42/42/42/42/42px |
| inbox/1440x900/loading.skeleton | PASS | 9 skeleton(s), heights=88/88/88/88/88/88/88/88/88px |
| queue/1440x900/loading.skeleton | PASS | 4 skeleton(s), heights=76/76/76/76px |
| board/390x844/loading.skeleton | PASS | 14 skeleton(s), heights=42/42/42/42/42/42/42/42/42/42/42/42/42/42px |
| inbox/390x844/loading.skeleton | PASS | 9 skeleton(s), heights=88/88/88/88/88/88/88/88/88px |
| queue/390x844/loading.skeleton | PASS | 4 skeleton(s), heights=76/76/76/76px |
| board/320x800/cards>=1 | PASS | 19 card(s) |
| board/320x800/AC1.padding=8 | PASS | all 19 cards 8/8/8/8 |
| board/320x800/AC1.textGap>=8 | PASS | min gap ok on 19 cards |
| board/320x800/AC1.statusBarOnGlyph | PASS | bar 3px 未压字（19 卡） |
| board/320x800/AC2.longTitle.lines<=3 | PASS | lines=3.01 lineHeight=23.55 |
| board/320x800/AC2.longTitle.clamped | PASS | scrollHeight>clientHeight=true |
| board/320x800/AC2.longTitle.noOverflowX | PASS | scrollWidth-clientWidth=0 |
| board/320x800/AC3.longToken.lines<=3 | PASS | lines=3.01 |
| board/320x800/AC3.longToken.noOverflowX | PASS | scrollWidth-clientWidth=0 |
| board/320x800/AC3.longToken.noDescendantOverflow | PASS | 越出 padding box 的后代=0 |
| board/320x800/AC4.cardFitsContainer | PASS | all cards <= parent width |
| board/320x800/AC4.metaNotClipped | PASS | meta items 无裁剪 |
| board/320x800/AC4.taskIdSingleLine | PASS | task_id 均单行 |
| board/320x800/AC4.taskIdTitleAttr | PASS | title 保留完整 task_id |
| board/320x800/innerOverflow=none | PASS | no descendant outside padding box |
| inbox/320x800/cards>=1 | PASS | 10 card(s) |
| inbox/320x800/AC1.padding=8 | PASS | all 10 cards 8/8/8/8 |
| inbox/320x800/AC1.textGap>=8 | PASS | min gap ok on 10 cards |
| inbox/320x800/AC1.statusBarOnGlyph | PASS | bar 3px 未压字（10 卡） |
| inbox/320x800/AC2.longTitle.lines<=3 | PASS | lines=3.01 lineHeight=23.55 |
| inbox/320x800/AC2.longTitle.clamped | PASS | scrollHeight>clientHeight=true |
| inbox/320x800/AC2.longTitle.noOverflowX | PASS | scrollWidth-clientWidth=0 |
| inbox/320x800/AC3.longToken.lines<=3 | PASS | lines=3.01 |
| inbox/320x800/AC3.longToken.noOverflowX | PASS | scrollWidth-clientWidth=0 |
| inbox/320x800/AC3.longToken.noDescendantOverflow | PASS | 越出 padding box 的后代=0 |
| inbox/320x800/AC4.cardFitsContainer | PASS | all cards <= parent width |
| inbox/320x800/AC4.metaNotClipped | PASS | meta items 无裁剪 |
| inbox/320x800/AC4.taskIdSingleLine | PASS | task_id 均单行 |
| inbox/320x800/AC4.taskIdTitleAttr | PASS | title 保留完整 task_id |
| inbox/320x800/innerOverflow=none | PASS | no descendant outside padding box |
| queue/320x800/cards>=1 | PASS | 6 card(s) |
| queue/320x800/AC1.padding=8 | PASS | all 6 cards 8/8/8/8 |
| queue/320x800/AC1.textGap>=8 | PASS | min gap ok on 6 cards |
| queue/320x800/AC1.statusBarOnGlyph | PASS | bar 3px 未压字（6 卡） |
| queue/320x800/AC2.longTitle.lines<=3 | PASS | lines=3.01 lineHeight=23.55 |
| queue/320x800/AC2.longTitle.clamped | PASS | scrollHeight>clientHeight=true |
| queue/320x800/AC2.longTitle.noOverflowX | PASS | scrollWidth-clientWidth=0 |
| queue/320x800/AC3.longToken.lines<=3 | PASS | lines=3.01 |
| queue/320x800/AC3.longToken.noOverflowX | PASS | scrollWidth-clientWidth=0 |
| queue/320x800/AC3.longToken.noDescendantOverflow | PASS | 越出 padding box 的后代=0 |
| queue/320x800/AC4.cardFitsContainer | PASS | all cards <= parent width |
| queue/320x800/AC4.metaNotClipped | PASS | meta items 无裁剪 |
| queue/320x800/AC4.taskIdSingleLine | PASS | task_id 均单行 |
| queue/320x800/AC4.taskIdTitleAttr | PASS | title 保留完整 task_id |
| queue/320x800/innerOverflow=none | PASS | no descendant outside padding box |
