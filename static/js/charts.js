import { Chart, BarController, BarElement, CategoryScale, LinearScale,
	DoughnutController, ArcElement, Tooltip, Legend } from 'chart.js'

Chart.register(BarController, BarElement, CategoryScale, LinearScale,
	DoughnutController, ArcElement, Tooltip, Legend)

window.DashboardChart = Chart
