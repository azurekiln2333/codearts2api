# R20 actual Linux CI failure repair

GitHub run40294063255 on75ed55c fails at Go tests; Ubuntu26 runner migration is a notice only. CNB main run cnb-7br-1k5rnfjli independently fails the same TestOverflowStorageFailureAndClosedSinkRemainNonBlocking assertion.

Root cause: stopped-consumer test bypassed Open normalization and left RetentionDays0. Snapshot cutoff then equals current time, discarding newly recorded metadata on a high-precision Linux clock. Production Open normalizes seven-day retention. Windows coarse clock previously masked the invalid fixture. A minute-old explicit timestamp reproduces the defect on Windows before normalization.

Repair normalizes fixture options, explicitly timestamps retained events inside retention, and separates elapsed/counter/window assertions. Exact9999 dropped,1 queued,2 retained and original one-second deadline remain. Storage failure and close assertions remain. No production changes.

Implementation verification: audit normal count20 and race count10 PASS. Parent metadata40/deployment28/actionlint PASS. Full integration and remote results recorded below after execution.

Independent Trellis check and parent full Go normal/race/vet/build PASS; diff check PASS. No remaining findings.
